package core

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/distribution/reference"
	digest "github.com/opencontainers/go-digest"
)

const (
	lockCoreNamespace      = ""
	lockOCILatestOperation = "oci-latest"
	lockOCISHAOperation    = "oci-sha"
	lockGitLatestOperation = "git-latest"
	lockGitSHAOperation    = "git-sha"
)

// UpdateWorkspaceLock refreshes the existing entries in a workspace lockfile in place.
func UpdateWorkspaceLock(ctx context.Context, query *Query, lock *workspace.Lock) error {
	entries, err := lock.Entries()
	if err != nil {
		return fmt.Errorf("read lock entries: %w", err)
	}

	var refreshedEntries []workspace.LookupEntry
	for _, entry := range entries {
		if !isLatestLockEntry(entry) {
			continue
		}

		result, err := updateWorkspaceLockEntry(ctx, query, entry)
		if err != nil {
			return err
		}
		if err := lock.SetLookup(entry.Namespace, entry.Operation, entry.Inputs, result); err != nil {
			return fmt.Errorf("rewrite lock entry for %s %v: %w", entry.Operation, entry.Inputs, err)
		}
		selectedEntry, err := selectedSHAEntry(entry, result)
		if err != nil {
			return err
		}
		if selectedEntry == nil {
			continue
		}
		if containsLockEntry(refreshedEntries, *selectedEntry) {
			continue
		}
		selectedResult, err := updateWorkspaceLockEntry(ctx, query, *selectedEntry)
		if err != nil {
			return err
		}
		if err := lock.SetLookup(
			selectedEntry.Namespace,
			selectedEntry.Operation,
			selectedEntry.Inputs,
			selectedResult,
		); err != nil {
			return fmt.Errorf(
				"write selected SHA entry for %s %v: %w",
				selectedEntry.Operation,
				selectedEntry.Inputs,
				err,
			)
		}
		refreshedEntries = append(refreshedEntries, *selectedEntry)
	}

	for _, entry := range entries {
		if isLatestLockEntry(entry) || containsLockEntry(refreshedEntries, entry) {
			continue
		}
		result, err := updateWorkspaceLockEntry(ctx, query, entry)
		if err != nil {
			return err
		}
		if err := lock.SetLookup(entry.Namespace, entry.Operation, entry.Inputs, result); err != nil {
			return fmt.Errorf("rewrite lock entry for %s %v: %w", entry.Operation, entry.Inputs, err)
		}
	}

	return nil
}

func isLatestLockEntry(entry workspace.LookupEntry) bool {
	return entry.Namespace == lockCoreNamespace &&
		(entry.Operation == lockGitLatestOperation ||
			entry.Operation == lockOCILatestOperation)
}

func containsLockEntry(entries []workspace.LookupEntry, target workspace.LookupEntry) bool {
	for _, entry := range entries {
		if entry.Namespace == target.Namespace &&
			entry.Operation == target.Operation &&
			reflect.DeepEqual(entry.Inputs, target.Inputs) {
			return true
		}
	}
	return false
}

func selectedSHAEntry(
	entry workspace.LookupEntry,
	result workspace.LookupResult,
) (*workspace.LookupEntry, error) {
	value, ok := result.Value.(string)
	if !ok || value == "" {
		return nil, fmt.Errorf("invalid %s lock value %v", entry.Operation, result.Value)
	}
	required, options, err := workspace.ParseLookupInputs(entry.Inputs)
	if err != nil {
		return nil, fmt.Errorf("invalid %s inputs %v: %w", entry.Operation, entry.Inputs, err)
	}

	var operation string
	var selectedInputs []any
	switch entry.Operation {
	case lockGitLatestOperation:
		if len(required) != 1 {
			return nil, fmt.Errorf("invalid %s inputs %v", entry.Operation, entry.Inputs)
		}
		operation = lockGitSHAOperation
		selectedInputs = []any{required[0], value}
	case lockOCILatestOperation:
		if len(required) != 1 {
			return nil, fmt.Errorf("invalid %s inputs %v", entry.Operation, entry.Inputs)
		}
		image, ok := required[0].(string)
		if !ok || image == "" {
			return nil, fmt.Errorf("invalid %s ref %v", entry.Operation, required[0])
		}
		ref, err := reference.ParseNormalizedNamed(image)
		if err != nil {
			return nil, fmt.Errorf("parse image address %q: %w", image, err)
		}
		ref, err = reference.WithTag(reference.TrimNamed(ref), value)
		if err != nil {
			return nil, fmt.Errorf("apply selected image tag %q: %w", value, err)
		}
		var shaOptions []workspace.LookupOption
		for name, optionValue := range options {
			if name == "includePrereleases" {
				continue
			}
			shaOptions = append(shaOptions, workspace.LookupOption{
				Name:  name,
				Value: optionValue,
			})
		}
		sort.Slice(shaOptions, func(i, j int) bool {
			return shaOptions[i].Name < shaOptions[j].Name
		})
		operation = lockOCISHAOperation
		selectedInputs = workspace.LookupInputs(
			[]any{ref.String()},
			shaOptions...,
		)
	default:
		return nil, nil
	}

	return &workspace.LookupEntry{
		Namespace: entry.Namespace,
		Operation: operation,
		Inputs:    selectedInputs,
		Result: workspace.LookupResult{
			Value:  value,
			Policy: workspace.PolicyPin,
		},
	}, nil
}

func updateWorkspaceLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	switch {
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockOCILatestOperation:
		return updateOCILatestLockEntry(ctx, query, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockOCISHAOperation:
		return updateOCISHALockEntry(ctx, query, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitLatestOperation:
		return updateGitLatestLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitSHAOperation:
		return updateGitSHALockEntry(ctx, entry)
	default:
		return workspace.LookupResult{}, fmt.Errorf("unsupported lock entry %q %q", entry.Namespace, entry.Operation)
	}
}

type ociLockInputs struct {
	ref                string
	includePrereleases bool
	registryTransport  serverresolver.RegistryTransport
}

func parseOCILockInputs(
	operation string,
	inputs []any,
	latest bool,
) (ociLockInputs, error) {
	var parsed ociLockInputs
	required, options, err := workspace.ParseLookupInputs(inputs)
	if err != nil {
		return parsed, fmt.Errorf("invalid %s inputs %v: %w", operation, inputs, err)
	}
	if len(required) != 1 {
		return parsed, fmt.Errorf("invalid %s inputs %v", operation, inputs)
	}
	ref, ok := required[0].(string)
	if !ok || ref == "" {
		return parsed, fmt.Errorf("invalid %s ref %v", operation, required[0])
	}
	refName, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return parsed, fmt.Errorf("invalid %s ref %q: %w", operation, ref, err)
	}
	if latest && !reference.IsNameOnly(refName) {
		return parsed, fmt.Errorf("invalid %s tagged ref %q", operation, ref)
	}
	if !latest {
		if _, ok := refName.(reference.NamedTagged); !ok {
			return parsed, fmt.Errorf("invalid %s untagged ref %q", operation, ref)
		}
	}
	parsed.ref = ref

	for name, value := range options {
		switch name {
		case "includePrereleases":
			if !latest {
				return parsed, fmt.Errorf("invalid %s option %q", operation, name)
			}
			include, ok := value.(bool)
			if !ok {
				return parsed, fmt.Errorf("invalid %s includePrereleases %v", operation, value)
			}
			parsed.includePrereleases = include
		case "protocol":
			protocol, ok := value.(string)
			if !ok {
				return parsed, fmt.Errorf("invalid %s registry protocol %v", operation, value)
			}
			switch serverresolver.RegistryProtocol(protocol) {
			case serverresolver.RegistryProtocolHTTP, serverresolver.RegistryProtocolHTTPS:
				parsed.registryTransport.Protocol = serverresolver.RegistryProtocol(protocol)
			default:
				return parsed, fmt.Errorf("invalid %s registry protocol %q", operation, protocol)
			}
		case "insecureSkipTLSVerify":
			insecure, ok := value.(bool)
			if !ok {
				return parsed, fmt.Errorf("invalid %s insecureSkipTLSVerify %v", operation, value)
			}
			parsed.registryTransport.InsecureSkipTLSVerify = insecure
		default:
			return parsed, fmt.Errorf("invalid %s option %q", operation, name)
		}
	}
	if parsed.registryTransport.InsecureSkipTLSVerify {
		if parsed.registryTransport.Protocol == serverresolver.RegistryProtocolHTTP {
			return parsed, fmt.Errorf("invalid %s registry transport options", operation)
		}
		if parsed.registryTransport.Protocol == "" {
			parsed.registryTransport.Protocol = serverresolver.RegistryProtocolHTTPS
		}
	}
	return parsed, nil
}

func updateOCILatestLockEntry(
	ctx context.Context,
	query *Query,
	entry workspace.LookupEntry,
) (workspace.LookupResult, error) {
	inputs, err := parseOCILockInputs(lockOCILatestOperation, entry.Inputs, true)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	refName, err := reference.ParseNormalizedNamed(inputs.ref)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("parse image address %q: %w", inputs.ref, err)
	}
	refName = reference.TrimNamed(refName)

	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("failed to get registry resolver: %w", err)
	}
	// Encapsulate so the tag listing's raw HTTP spans stay out of the default
	// TUI; they surface if the listing fails.
	listCtx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("select latest release for %s", refName.String()),
		telemetry.Internal(), telemetry.Encapsulate())
	tags, err := rslvr.ListImageTags(listCtx, refName.String(), serverresolver.ListImageTagsOpts{
		RegistryTransport: inputs.registryTransport,
	})
	telemetry.EndWithCause(span, &err)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("list image tags for %q: %w", refName.String(), err)
	}
	selectedTag, err := SelectLatestContainerTag(tags, inputs.includePrereleases)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("select latest image tag for %q: %w", refName.String(), err)
	}
	return workspace.LookupResult{
		Value:  selectedTag,
		Policy: workspace.PolicyPin,
	}, nil
}

func updateOCISHALockEntry(
	ctx context.Context,
	query *Query,
	entry workspace.LookupEntry,
) (workspace.LookupResult, error) {
	inputs, err := parseOCILockInputs(lockOCISHAOperation, entry.Inputs, false)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	resolvedDigest, err := resolveOCIDigest(ctx, query, inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{
		Value:  resolvedDigest.String(),
		Policy: entry.Result.Policy,
	}, nil
}

func resolveOCIDigest(ctx context.Context, query *Query, inputs ociLockInputs) (digest.Digest, error) {
	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get registry resolver: %w", err)
	}

	refName, err := reference.ParseNormalizedNamed(inputs.ref)
	if err != nil {
		return "", fmt.Errorf("parse image address %q: %w", inputs.ref, err)
	}
	refName = reference.TagNameOnly(refName)

	_, resolvedDigest, err := rslvr.ResolveImageDigest(ctx, refName.String(), serverresolver.ResolveImageDigestOpts{
		RegistryTransport: inputs.registryTransport,
	})
	if err != nil {
		return "", fmt.Errorf("resolve image %q: %w", refName.String(), err)
	}

	return resolvedDigest, nil
}

func updateGitSHALockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	remoteURL, name, err := parseGitLookupInputs(lockGitSHAOperation, entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	result, err := resolveGitSHA(ctx, remoteURL, name)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: result, Policy: entry.Result.Policy}, nil
}

func parseGitLookupInputs(operation string, inputs []any) (string, string, error) {
	required, options, err := workspace.ParseLookupInputs(inputs)
	if err != nil {
		return "", "", fmt.Errorf("invalid %s inputs %v: %w", operation, inputs, err)
	}
	if len(required) != 2 || len(options) != 0 {
		return "", "", fmt.Errorf("invalid %s inputs %v", operation, inputs)
	}
	remoteURL, ok := required[0].(string)
	if !ok || remoteURL == "" {
		return "", "", fmt.Errorf("invalid %s remote %v", operation, inputs[0])
	}
	name, ok := required[1].(string)
	if !ok || name == "" {
		return "", "", fmt.Errorf("invalid %s name %v", operation, inputs[1])
	}
	return remoteURL, name, nil
}

func resolveGitSHA(ctx context.Context, remoteURL, name string) (string, error) {
	if gitutil.IsCommitSHA(name) {
		return name, nil
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return "", err
	}

	var ref dagql.ObjectResult[*GitRef]
	if err := srv.Select(ctx, srv.Root(), &ref,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(remoteURL)},
			},
		},
		dagql.Selector{
			Field: "ref",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)},
			},
		},
	); err != nil {
		return "", fmt.Errorf("resolve git ref %q for %q: %w", name, remoteURL, err)
	}
	return ref.Self().Ref.SHA, nil
}

func updateGitLatestLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	required, options, err := workspace.ParseLookupInputs(entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf(
			"invalid %s inputs %v: %w",
			lockGitLatestOperation,
			entry.Inputs,
			err,
		)
	}
	if len(required) != 1 {
		return workspace.LookupResult{}, fmt.Errorf(
			"invalid %s inputs %v",
			lockGitLatestOperation,
			entry.Inputs,
		)
	}
	remoteURL, ok := required[0].(string)
	if !ok || remoteURL == "" {
		return workspace.LookupResult{}, fmt.Errorf(
			"invalid %s remote %v",
			lockGitLatestOperation,
			required[0],
		)
	}
	var includePrereleases bool
	var tagPrefix string
	for name, value := range options {
		switch name {
		case "includePrereleases":
			var ok bool
			includePrereleases, ok = value.(bool)
			if !ok {
				return workspace.LookupResult{}, fmt.Errorf(
					"invalid %s includePrereleases %v",
					lockGitLatestOperation,
					value,
				)
			}
		case "tagPrefix":
			var ok bool
			tagPrefix, ok = value.(string)
			if !ok || tagPrefix == "" {
				return workspace.LookupResult{}, fmt.Errorf(
					"invalid %s tagPrefix %v",
					lockGitLatestOperation,
					value,
				)
			}
		default:
			return workspace.LookupResult{}, fmt.Errorf(
				"invalid %s option %q",
				lockGitLatestOperation,
				name,
			)
		}
	}

	// Resolve through the schema's git resolver rather than a bare
	// RemoteGitRepository so the same access context that created the pin
	// (credential helpers, SSH sockets, protocol fallback) applies here too.
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	latestInputs := []dagql.NamedInput{
		{Name: "includeSubreleases", Value: dagql.Boolean(includePrereleases)},
	}
	if tagPrefix != "" {
		latestInputs = append(latestInputs, dagql.NamedInput{
			Name:  "tagPrefix",
			Value: dagql.String(tagPrefix),
		})
	}

	var latest dagql.ObjectResult[*GitRef]
	if err := srv.Select(ctx, srv.Root(), &latest,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(remoteURL)},
			},
		},
		dagql.Selector{
			Field: "latest",
			Args:  latestInputs,
		},
	); err != nil {
		return workspace.LookupResult{}, fmt.Errorf("resolve latest git release for %q: %w", remoteURL, err)
	}

	selectedRef := latest.Self().Ref.Name
	return workspace.LookupResult{Value: selectedRef, Policy: workspace.PolicyPin}, nil
}
