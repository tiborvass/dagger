package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectLatestContainerTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name               string
		tags               []string
		includeSubreleases bool
		want               string
	}{
		{
			name: "stable release",
			tags: []string{"latest", "edge", "v1.9.0", "2.0.0", "v3.0.0-rc.1"},
			want: "2.0.0",
		},
		{
			name: "equivalent versions deterministic",
			tags: []string{"1.2.3", "v1.2.3"},
			want: "v1.2.3",
		},
		{
			name: "only prereleases",
			tags: []string{"edge", "v2.0.0-rc.1"},
			want: "latest",
		},
		{
			name:               "include prereleases",
			tags:               []string{"2.0.0", "v3.0.0-alpha.2", "v3.0.0-beta.1"},
			includeSubreleases: true,
			want:               "v3.0.0-beta.1",
		},
		{
			name: "no tags",
			want: "latest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, SelectLatestContainerTag(tc.tags, tc.includeSubreleases))
		})
	}
}

func TestValidateContainerLatestTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name               string
		tag                string
		includeSubreleases bool
		wantErr            string
	}{
		{name: "stable", tag: "3.22.1"},
		{name: "stable with v", tag: "v3.22.1"},
		{name: "latest fallback", tag: "latest"},
		{name: "prerelease", tag: "v4.0.0-rc.1", wantErr: "not a stable semantic version"},
		{name: "included prerelease", tag: "v4.0.0-rc.1", includeSubreleases: true},
		{name: "non-semver", tag: "edge", wantErr: "not a semantic version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateContainerLatestTag(tc.tag, tc.includeSubreleases)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
