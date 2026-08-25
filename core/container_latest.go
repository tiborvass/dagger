package core

import (
	"fmt"

	"golang.org/x/mod/semver"
)

// SelectLatestContainerTag returns the greatest eligible release tag after
// normalizing optional v prefixes, incomplete versions, and zero-padded numeric
// components. If tags contains no eligible release, it returns "latest".
func SelectLatestContainerTag(tags []string, includeSubreleases bool) (string, error) {
	candidates := make([]releaseTagCandidate, 0, len(tags))
	for _, tag := range tags {
		candidates = append(candidates, releaseTagCandidate{
			Original: tag,
			Version:  tag,
		})
	}
	selected, found, err := selectLatestReleaseTag(
		candidates,
		includeSubreleases,
		releaseTagTiePreferComplete,
	)
	if err != nil {
		return "", err
	}
	if !found {
		return "latest", nil
	}
	return selected.Original, nil
}

// ValidateContainerLatestTag validates a tag selected by oci-latest.
func ValidateContainerLatestTag(tag string, includePrereleases bool) error {
	if tag == "latest" {
		return nil
	}
	normalized, ok := normalizeReleaseTag(tag, tag)
	if !ok {
		return fmt.Errorf("tag %q is not a semantic version", tag)
	}
	if !includePrereleases && semver.Prerelease(normalized.Normalized) != "" {
		return fmt.Errorf("tag %q is not a stable semantic version", tag)
	}
	return nil
}
