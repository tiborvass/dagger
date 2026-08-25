package core

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

type releaseTagCandidate struct {
	Original     string
	Version      string
	Normalized   string
	HasVPrefix   bool
	NumericParts []string
	Prerelease   string
	Build        string
}

type releaseTagTiePolicy int

const (
	// Git tags are exact names, so every distinct spelling at the winning
	// normalized version is ambiguous.
	releaseTagTieStrict releaseTagTiePolicy = iota
	// OCI tags with omitted trailing zero components conventionally float over
	// that release line. Prefer the more precise spelling when both are present.
	releaseTagTiePreferComplete
)

func normalizeReleaseTag(original, version string) (releaseTagCandidate, bool) {
	originalVersion := version
	hasVPrefix := strings.HasPrefix(version, "v")
	version = strings.TrimPrefix(version, "v")
	core := version
	build := ""
	if buildIndex := strings.IndexByte(core, '+'); buildIndex >= 0 {
		build = core[buildIndex:]
		core = core[:buildIndex]
	}
	prerelease := ""
	if prereleaseIndex := strings.IndexByte(core, '-'); prereleaseIndex >= 0 {
		prerelease = core[prereleaseIndex:]
		core = core[:prereleaseIndex]
	}

	numericParts := strings.Split(core, ".")
	parts := slices.Clone(numericParts)
	if len(parts) == 0 || len(parts) > 3 {
		return releaseTagCandidate{}, false
	}
	for i, part := range parts {
		if part == "" || strings.IndexFunc(part, func(r rune) bool {
			return r < '0' || r > '9'
		}) >= 0 {
			return releaseTagCandidate{}, false
		}
		part = strings.TrimLeft(part, "0")
		if part == "" {
			part = "0"
		}
		parts[i] = part
	}
	for len(parts) < 3 {
		parts = append(parts, "0")
	}

	candidate := "v" + strings.Join(parts, ".") + prerelease + build
	if !semver.IsValid(candidate) {
		return releaseTagCandidate{}, false
	}
	return releaseTagCandidate{
		Original:     original,
		Version:      originalVersion,
		Normalized:   semver.Canonical(candidate),
		HasVPrefix:   hasVPrefix,
		NumericParts: numericParts,
		Prerelease:   prerelease,
		Build:        build,
	}, true
}

func selectLatestReleaseTag(
	candidates []releaseTagCandidate,
	includePrereleases bool,
	tiePolicy releaseTagTiePolicy,
) (releaseTagCandidate, bool, error) {
	var best releaseTagCandidate
	var equivalent []releaseTagCandidate
	for _, candidate := range candidates {
		normalized, ok := normalizeReleaseTag(candidate.Original, candidate.Version)
		if !ok || (!includePrereleases && semver.Prerelease(normalized.Normalized) != "") {
			continue
		}

		comparison := semver.Compare(normalized.Normalized, best.Normalized)
		switch {
		case best.Original == "" || comparison > 0:
			best = normalized
			equivalent = []releaseTagCandidate{normalized}
		case comparison == 0 && normalized.Normalized == best.Normalized:
			if !slices.ContainsFunc(equivalent, func(candidate releaseTagCandidate) bool {
				return candidate.Original == normalized.Original
			}) {
				equivalent = append(equivalent, normalized)
			}
		}
	}

	if best.Original == "" {
		return releaseTagCandidate{}, false, nil
	}
	if tiePolicy == releaseTagTiePreferComplete {
		equivalent = discardIncompleteReleaseAliases(equivalent)
	}
	if len(equivalent) > 1 {
		originals := make([]string, len(equivalent))
		for i, candidate := range equivalent {
			originals[i] = candidate.Original
		}
		slices.Sort(originals)
		return releaseTagCandidate{}, false, fmt.Errorf(
			"ambiguous latest release %s: equivalent tags %q",
			best.Normalized,
			originals,
		)
	}
	return equivalent[0], true, nil
}

func discardIncompleteReleaseAliases(
	candidates []releaseTagCandidate,
) []releaseTagCandidate {
	return slices.DeleteFunc(slices.Clone(candidates), func(candidate releaseTagCandidate) bool {
		return slices.ContainsFunc(candidates, func(other releaseTagCandidate) bool {
			return isIncompleteReleaseAlias(candidate, other)
		})
	})
}

func isIncompleteReleaseAlias(
	candidate releaseTagCandidate,
	other releaseTagCandidate,
) bool {
	if candidate.HasVPrefix != other.HasVPrefix ||
		candidate.Prerelease != other.Prerelease ||
		candidate.Build != other.Build ||
		len(candidate.NumericParts) >= len(other.NumericParts) {
		return false
	}
	for i, part := range candidate.NumericParts {
		if part != other.NumericParts[i] {
			return false
		}
	}
	for _, part := range other.NumericParts[len(candidate.NumericParts):] {
		if part != "0" {
			return false
		}
	}
	return true
}
