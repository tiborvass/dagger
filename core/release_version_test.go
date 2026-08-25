package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeReleaseTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		tag        string
		normalized string
		valid      bool
	}{
		{name: "canonical semver", tag: "v1.2.3", normalized: "v1.2.3", valid: true},
		{name: "optional v prefix", tag: "1.2.3", normalized: "v1.2.3", valid: true},
		{name: "incomplete minor", tag: "v1.2", normalized: "v1.2.0", valid: true},
		{name: "incomplete major", tag: "v1", normalized: "v1.0.0", valid: true},
		{name: "calver", tag: "24.04", normalized: "v24.4.0", valid: true},
		{name: "zero-padded version", tag: "v01.002.0003", normalized: "v1.2.3", valid: true},
		{name: "build metadata", tag: "v1.2.3+linux-amd64", normalized: "v1.2.3", valid: true},
		{name: "prerelease", tag: "v1.2-rc.1", normalized: "v1.2.0-rc.1", valid: true},
		{name: "empty component", tag: "v1..2"},
		{name: "too many components", tag: "v1.2.3.4"},
		{name: "empty prerelease", tag: "v1.2.3-"},
		{name: "non-version", tag: "latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := normalizeReleaseTag(tc.tag, tc.tag)
			require.Equal(t, tc.valid, ok)
			if ok {
				require.Equal(t, tc.normalized, got.Normalized)
				require.Equal(t, tc.tag, got.Original)
			}
		})
	}
}

func TestSelectLatestReleaseTagIgnoresLowerAmbiguity(t *testing.T) {
	t.Parallel()

	selected, found, err := selectLatestReleaseTag([]releaseTagCandidate{
		{Original: "v1.2", Version: "v1.2"},
		{Original: "v1.2.0", Version: "v1.2.0"},
		{Original: "v2.0.0", Version: "v2.0.0"},
	}, false, releaseTagTieStrict)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "v2.0.0", selected.Original)
}

func TestSelectLatestReleaseTagReportsOriginalTags(t *testing.T) {
	t.Parallel()

	_, _, err := selectLatestReleaseTag([]releaseTagCandidate{
		{Original: "24.04", Version: "24.04"},
		{Original: "v24.4.0", Version: "v24.4.0"},
	}, false, releaseTagTieStrict)
	require.ErrorContains(t, err, `equivalent tags ["24.04" "v24.4.0"]`)
}

func TestNormalizeReleaseTagPreservesPrefixedOriginal(t *testing.T) {
	t.Parallel()

	got, ok := normalizeReleaseTag("module/v24.04", "v24.04")
	require.True(t, ok)
	require.Equal(t, "module/v24.04", got.Original)
	require.Equal(t, "v24.04", got.Version)
	require.Equal(t, "v24.4.0", got.Normalized)
}
