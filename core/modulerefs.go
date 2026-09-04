package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
)

// ErrModuleVersionNotFound indicates that a requested module version does not
// match any tag in its Git repository.
var ErrModuleVersionNotFound = errors.New("module version not found")

// FastModuleSourceKindCheck performs a quick heuristic check to determine
// whether a module ref string refers to a local path or a git source.
// Returns "" if the kind cannot be determined without further inspection.
func FastModuleSourceKindCheck(
	refString string,
	refPin string,
) ModuleSourceKind {
	switch gitref.FastKindCheck(refString, refPin) {
	case gitref.KindLocal:
		return ModuleSourceKindLocal
	case gitref.KindGit:
		return ModuleSourceKindGit
	default:
		return ""
	}
}

// GitRefString builds a module ref string from a clone ref, an optional source
// root subpath and an optional version.
func GitRefString(cloneRef, sourceRootSubpath, version string) string {
	return gitref.RefString(cloneRef, sourceRootSubpath, version)
}

// SourceRef is a parsed local or remote source reference. Module and workspace
// loaders share this syntax and apply their own path semantics after parsing.
type SourceRef struct {
	Kind   ModuleSourceKind
	Local  *LocalSourceRef
	Remote *RemoteSourceRef
}

func ParseSourceRef(
	ctx context.Context,
	statFS StatFS,
	refString string,
	refPin string,
) (_ *SourceRef, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("parseRefString: %s", refString), telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

	kind := FastModuleSourceKindCheck(refString, refPin)
	switch kind {
	case ModuleSourceKindLocal:
		return &SourceRef{
			Kind: kind,
			Local: &LocalSourceRef{
				Path: refString,
			},
		}, nil
	case ModuleSourceKindGit:
		refString = resolveDaggerGetRedirect(ctx, refString)
		parsedRemoteRef, err := ParseRemoteSourceRef(ctx, refString)
		if err != nil {
			return nil, fmt.Errorf("failed to parse git ref string: %w", err)
		}
		return &SourceRef{
			Kind:   kind,
			Remote: &parsedRemoteRef,
		}, nil
	}

	// First, we stat ref in case the mod path github.com/username is a local directory
	if _, stat, err := statFS.Stat(ctx, refString); err != nil {
		slog.Debug("parseRefString stat error", "error", err)
	} else if stat.IsDir() {
		return &SourceRef{
			Kind: ModuleSourceKindLocal,
			Local: &LocalSourceRef{
				Path: refString,
			},
		}, nil
	}

	// Parse scheme and attempt to parse as git endpoint
	refString = resolveDaggerGetRedirect(ctx, refString)
	parsedRemoteRef, err := ParseRemoteSourceRef(ctx, refString)
	switch {
	case err == nil:
		return &SourceRef{
			Kind:   ModuleSourceKindGit,
			Remote: &parsedRemoteRef,
		}, nil
	case errors.As(err, &gitref.EndpointError{}):
		// couldn't connect to git endpoint, fallback to local
		return &SourceRef{
			Kind: ModuleSourceKindLocal,
			Local: &LocalSourceRef{
				Path: refString,
			},
		}, nil
	default:
		return nil, fmt.Errorf("failed to parse ref string: %w", err)
	}
}

type LocalSourceRef struct {
	Path string
}

// RemoteSourceRef pairs the loadable, versionless source URL and version with
// the parsed Git data needed by remote source loaders.
type RemoteSourceRef struct {
	URL string
	gitref.Parsed
}

func ParseRemoteSourceRef(ctx context.Context, refString string) (RemoteSourceRef, error) {
	parsed, err := gitref.Parse(ctx, refString)
	if err != nil {
		return RemoteSourceRef{}, err
	}
	return RemoteSourceRef{
		URL:    gitref.RefString(parsed.SourceCloneRef, parsed.RepoRootSubdir, ""),
		Parsed: parsed,
	}, nil
}

func (p *RemoteSourceRef) GitRef(
	ctx context.Context,
	dag *dagql.Server,
	pinCommitRef string, // "" if none
) (inst dagql.ObjectResult[*GitRef], rerr error) {
	pinIsSHA := gitutil.IsCommitSHA(pinCommitRef)

	withCommitArg := func(selector dagql.Selector) dagql.Selector {
		if pinIsSHA {
			selector.Args = append(selector.Args, dagql.NamedInput{Name: "commit", Value: dagql.String(pinCommitRef)})
		}
		return selector
	}

	var modTag string
	if p.HasVersion && semver.IsValid(p.Version) {
		var tags dagql.Array[dagql.String]
		err := dag.Select(ctx, dag.Root(), &tags,
			dagql.Selector{
				Field: "git",
				Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(p.CloneRef)},
				},
			},
			dagql.Selector{
				Field: "tags",
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to resolve git tags: %w", err)
		}

		allTags := make([]string, len(tags))
		for i, tag := range tags {
			allTags[i] = tag.String()
		}

		matched, err := matchVersion(allTags, p.Version, p.RepoRootSubdir)
		if err != nil {
			return inst, fmt.Errorf("matching version to tags: %w", err)
		}
		modTag = matched
	}

	repoSelector := dagql.Selector{
		Field: "git",
		Args: []dagql.NamedInput{
			{Name: "url", Value: dagql.String(p.CloneRef)},
		},
	}
	repoSelector = withCommitArg(repoSelector)

	refSelector := moduleGitDefaultRefSelector(ctx, p)
	switch {
	case modTag != "":
		refSelector = withCommitArg(dagql.Selector{
			Field: "tag",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(modTag)},
			},
		})
	case p.HasVersion:
		refSelector = withCommitArg(dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(p.Version)},
			},
		})
	case pinCommitRef != "" && !pinIsSHA:
		refSelector = dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(pinCommitRef)},
			},
		}
	case pinIsSHA:
		// A module config pin is authoritative over the consuming workspace's
		// git-sha lock entries. Pass it through ref's internal commit argument so
		// ref resolution cannot replay a stale HEAD lock entry.
		refSelector = withCommitArg(dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String("HEAD")},
			},
		})
	}

	var gitRef dagql.ObjectResult[*GitRef]
	err := dag.Select(ctx, dag.Root(), &gitRef, repoSelector, refSelector)
	if err != nil {
		return inst, fmt.Errorf("failed to resolve git src: %w", err)
	}

	return gitRef, nil
}

func moduleGitDefaultRefSelector(
	ctx context.Context,
	p *RemoteSourceRef,
) dagql.Selector {
	if !Supports(ctx, workspace.LatestReleaseVersion) {
		return dagql.Selector{Field: "head"}
	}

	selector := dagql.Selector{Field: "latest"}
	if p.RepoRootSubdir != "/" {
		selector.Args = append(selector.Args, dagql.NamedInput{
			Name:  "tagPrefix",
			Value: dagql.String(strings.Trim(p.RepoRootSubdir, "/")),
		})
	}
	return selector
}

// Match a version string in a list of versions with optional subPath
// e.g. github.com/foo/daggerverse/mod@mod/v1.0.0
// e.g. github.com/foo/mod@v1.0.0
// TODO smarter matching logic, e.g. v1 == v1.0.0
func matchVersion(versions []string, match, subPath string) (string, error) {
	// If theres a subPath, first match on {subPath}/{match} for monorepo tags
	if subPath != "/" {
		rawSubPath, _ := strings.CutPrefix(subPath, "/")
		matched, err := matchVersion(versions, fmt.Sprintf("%s/%s", rawSubPath, match), "/")
		// no error means there's a match with subpath/match
		if err == nil {
			return matched, nil
		}
	}

	for _, v := range versions {
		if v == match {
			return v, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrModuleVersionNotFound, match)
}
