package core

import (
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestRewriteRemoteForVarnish(t *testing.T) {
	t.Setenv(gitVarnishEnvName, "http://127.0.0.1:6084")

	cases := []struct {
		name   string
		rawURL string
		want   string
	}{
		{
			name:   "rewrite github https",
			rawURL: "https://github.com/dagger/dagger.git",
			want:   "http://127.0.0.1:6084/github.com/dagger/dagger.git",
		},
		{
			name:   "rewrite github http",
			rawURL: "http://github.com/dagger/dagger.git",
			want:   "http://127.0.0.1:6084/github.com/dagger/dagger.git",
		},
		{
			name:   "preserve query",
			rawURL: "https://github.com/dagger/dagger.git?foo=bar",
			want:   "http://127.0.0.1:6084/github.com/dagger/dagger.git?foo=bar",
		},
		{
			name:   "skip ssh",
			rawURL: "ssh://git@github.com/dagger/dagger.git",
			want:   "ssh://git@github.com/dagger/dagger.git",
		},
		{
			name:   "skip non-github host",
			rawURL: "https://gitlab.com/dagger/dagger.git",
			want:   "https://gitlab.com/dagger/dagger.git",
		},
		{
			name:   "skip userinfo",
			rawURL: "https://user:pass@github.com/dagger/dagger.git",
			want:   "https://user:pass@github.com/dagger/dagger.git",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := gitutil.ParseURL(tc.rawURL)
			require.NoError(t, err)
			repo := &RemoteGitRepository{URL: parsed}
			require.Equal(t, tc.want, maybeRewriteRemoteForVarnish(repo))
		})
	}
}

func TestRewriteRemoteForVarnishDisabled(t *testing.T) {
	parsed, err := gitutil.ParseURL("https://github.com/dagger/dagger.git")
	require.NoError(t, err)

	repo := &RemoteGitRepository{URL: parsed}
	require.Equal(t, parsed.Remote(), maybeRewriteRemoteForVarnish(repo))
}

func TestRewriteRemoteForVarnishWithEndpointPath(t *testing.T) {
	t.Setenv(gitVarnishEnvName, "http://127.0.0.1:6084/cache")
	parsed, err := gitutil.ParseURL("https://github.com/dagger/dagger.git")
	require.NoError(t, err)

	repo := &RemoteGitRepository{URL: parsed}
	require.Equal(t, "http://127.0.0.1:6084/cache/github.com/dagger/dagger.git", maybeRewriteRemoteForVarnish(repo))
}

func TestRewriteRemoteForVarnishInvalidEndpoint(t *testing.T) {
	t.Setenv(gitVarnishEnvName, "://invalid")
	parsed, err := gitutil.ParseURL("https://github.com/dagger/dagger.git")
	require.NoError(t, err)

	repo := &RemoteGitRepository{URL: parsed}
	require.Equal(t, parsed.Remote(), maybeRewriteRemoteForVarnish(repo))
}
