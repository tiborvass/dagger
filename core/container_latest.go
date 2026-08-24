package core

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// SelectLatestContainerTag returns the greatest eligible semantic-version tag.
// If tags contains no eligible release, it returns the literal latest tag.
func SelectLatestContainerTag(tags []string, includeSubreleases bool) string {
	var bestTag, bestVersion string
	for _, tag := range tags {
		version := tag
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if !semver.IsValid(version) || (!includeSubreleases && semver.Prerelease(version) != "") {
			continue
		}

		comparison := semver.Compare(version, bestVersion)
		if bestTag == "" || comparison > 0 || (comparison == 0 && tag > bestTag) {
			bestTag = tag
			bestVersion = version
		}
	}
	if bestTag == "" {
		return "latest"
	}
	return bestTag
}

// ValidateContainerLatestTag validates a tag selected by oci-latest.
func ValidateContainerLatestTag(tag string, includePrereleases bool) error {
	if tag == "latest" {
		return nil
	}
	version := tag
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return fmt.Errorf("tag %q is not a semantic version", tag)
	}
	if !includePrereleases && semver.Prerelease(version) != "" {
		return fmt.Errorf("tag %q is not a stable semantic version", tag)
	}
	return nil
}
