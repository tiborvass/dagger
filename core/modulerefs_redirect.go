package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

const (
	// daggerGetQueryParam is the query flag appended to a module ref to probe
	// for a redirect that points at the real module source.
	daggerGetQueryParam = "dagger-get"

	// daggerGetProbeTimeout bounds the redirect probe so a slow or hanging host
	// cannot block module resolution.
	daggerGetProbeTimeout = 5 * time.Second
)

// daggerGetClient issues the redirect probe. It never follows redirects itself:
// we must read the Location header and rewrite it (stripping dagger-get,
// re-appending any version) before continuing resolution.
var daggerGetClient = &http.Client{
	Timeout: daggerGetProbeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// resolveDaggerGetRedirect implements the module redirect mechanism: for https
// (or schemeless, attempted over https) module refs, it fetches
// "<ref>?dagger-get=1" and, if the host answers with a single 3xx pointing at
// an absolute https Location, continues resolution with that destination. Any
// "@version"/"@sha" suffix is stripped before the probe and re-appended to the
// destination. Git-protocol, http, and ssh refs are left untouched.
//
// On any non-redirect outcome (non-3xx status, missing/invalid Location,
// timeout, or transport error) it falls back to the original ref. The result is
// cached per session so a ref is probed at most once per session.
func resolveDaggerGetRedirect(ctx context.Context, refString string) string {
	if !daggerGetEligible(refString) {
		return refString
	}

	cache, cacheErr := dagql.EngineCache(ctx)
	clientMetadata, mdErr := engine.ClientMetadataFromContext(ctx)
	if cacheErr != nil || mdErr != nil {
		// No session cache available; probe directly.
		return daggerGetProbe(ctx, refString)
	}

	res, err := cache.GetOrInitArbitrary(
		ctx,
		clientMetadata.SessionID,
		"module-dagger-get-redirect:"+refString,
		func(ctx context.Context) (any, error) {
			return daggerGetProbe(ctx, refString), nil
		},
	)
	if err != nil {
		slog.Debug("dagger-get redirect cache error; probing directly", "ref", refString, "error", err)
		return daggerGetProbe(ctx, refString)
	}
	if resolved, ok := res.Value().(string); ok && resolved != "" {
		return resolved
	}
	return refString
}

// daggerGetEligible reports whether the redirect probe applies to refString.
// Only https and schemeless refs qualify; local paths, SCP-like refs, and
// explicit non-https schemes (http, ssh, git) are excluded.
func daggerGetEligible(refString string) bool {
	if refString == "" || refString[0] == '/' || refString[0] == '.' {
		return false
	}
	if strings.Contains(refString, "://") && !strings.HasPrefix(refString, gitref.SchemeHTTPS.Prefix()) {
		return false
	}
	switch gitref.Scheme(refString) {
	case gitref.SchemeHTTPS:
		return true
	case gitref.NoScheme:
		// Schemeless refs are attempted over https; require a hostname with a
		// dot so we don't probe obvious non-URLs.
		host := refString
		if i := strings.IndexAny(host, "/@:"); i >= 0 {
			host = host[:i]
		}
		return strings.Contains(host, ".")
	default:
		return false
	}
}

// daggerGetProbe performs the actual single-hop redirect probe and returns the
// resolved ref, or the original ref on any non-redirect outcome.
func daggerGetProbe(ctx context.Context, refString string) string {
	// Module refs spell versions with "@" (and historically "#"); normalize so
	// url.Parse doesn't treat a version as a URL fragment.
	normalized := strings.Replace(refString, "#", "@", 1)
	if !strings.HasPrefix(normalized, gitref.SchemeHTTPS.Prefix()) {
		normalized = gitref.SchemeHTTPS.Prefix() + normalized
	}

	u, err := url.Parse(normalized)
	if err != nil {
		return refString
	}

	// Strip a path-level "@version"; userinfo "@" stays in u.User, not u.Path.
	version := ""
	if i := strings.Index(u.Path, "@"); i >= 0 {
		version = u.Path[i+1:]
		u.Path = u.Path[:i]
	}

	probe := *u
	probe.RawQuery = daggerGetQueryParam + "=1"
	probe.Fragment = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.String(), nil)
	if err != nil {
		return refString
	}
	resp, err := daggerGetClient.Do(req)
	if err != nil {
		slog.Debug("dagger-get probe failed; using original ref", "url", probe.String(), "error", err)
		return refString
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusMultipleChoices || resp.StatusCode >= http.StatusBadRequest {
		// Not a 3xx: no redirect configured for this ref.
		return refString
	}

	loc := resp.Header.Get("Location")
	if loc == "" {
		return refString
	}
	locURL, err := url.Parse(loc)
	if err != nil || locURL.Scheme != "https" || locURL.Host == "" {
		slog.Debug("dagger-get redirect ignored: Location is not an absolute https URL",
			"ref", refString, "location", loc)
		return refString
	}

	// Drop only the dagger-get param the server may have echoed back.
	q := locURL.Query()
	q.Del(daggerGetQueryParam)
	locURL.RawQuery = q.Encode()

	// Re-append the version into the destination path (before any query) so the
	// downstream git-ref parser reads it as a version, not a query value.
	if version != "" {
		base := strings.TrimSuffix(locURL.Path, "/")
		if base == "" {
			base = "/"
		}
		locURL.Path = base + "@" + version
	}

	resolved := locURL.String()
	slog.Debug("dagger-get redirect resolved", "from", refString, "to", resolved)
	return resolved
}
