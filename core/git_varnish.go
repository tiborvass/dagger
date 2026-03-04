package core

import (
	"net/url"
	"os"
	"path"

	"github.com/dagger/dagger/util/gitutil"
)

const (
	gitVarnishEnvName = "DAGGER_VARNISH"
	githubHostName    = "github.com"
)

func maybeRewriteRemoteForVarnish(repo *RemoteGitRepository) string {
	if repo == nil || repo.URL == nil {
		return ""
	}

	remote := repo.URL
	endpoint := os.Getenv(gitVarnishEnvName)
	if endpoint == "" {
		return remote.Remote()
	}

	if remote.User != nil {
		return remote.Remote()
	}
	if remote.Scheme != gitutil.HTTPProtocol && remote.Scheme != gitutil.HTTPSProtocol {
		return remote.Remote()
	}
	if remote.Host != githubHostName {
		return remote.Remote()
	}
	if repo.AuthToken.Self() != nil || repo.AuthHeader.Self() != nil || repo.SSHAuthSocket.Self() != nil {
		return remote.Remote()
	}

	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Scheme == "" || endpointURL.Host == "" {
		return remote.Remote()
	}

	rewritten := *endpointURL
	rewritten.Path = joinURLPath(rewritten.Path, path.Join("/", remote.Host, remote.Path))
	if parsed, err := url.Parse(remote.Remote()); err == nil {
		rewritten.RawQuery = parsed.RawQuery
	}

	return rewritten.String()
}

func joinURLPath(base, suffix string) string {
	if suffix == "" {
		if base == "" {
			return "/"
		}
		return base
	}

	if base == "" || base == "/" {
		return suffix
	}

	return path.Join(base, suffix)
}
