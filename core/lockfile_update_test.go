package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/stretchr/testify/require"
)

func TestUpdateWorkspaceLockEntry(t *testing.T) {
	t.Parallel()

	_, err := updateWorkspaceLockEntry(context.Background(), nil, workspace.LookupEntry{
		Namespace: "acme",
		Operation: "resolve",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported lock entry "acme" "resolve"`)
}

func TestUpdateGitLatestLockEntryValidatesInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		inputs  []any
		wantErr string
	}{
		{name: "missing inputs", wantErr: "invalid git.latest inputs"},
		{name: "invalid remote type", inputs: []any{42, false}, wantErr: "invalid git.latest remote"},
		{name: "empty remote", inputs: []any{"", false}, wantErr: "invalid git.latest remote"},
		{name: "invalid policy", inputs: []any{"https://example.com/repo.git", "false"}, wantErr: "invalid git.latest includeSubreleases"},
		{name: "invalid prefix type", inputs: []any{"https://example.com/repo.git", false, 42}, wantErr: "invalid git.latest tag prefix"},
		{name: "empty prefix", inputs: []any{"https://example.com/repo.git", false, ""}, wantErr: "invalid git.latest tag prefix"},
		{name: "extra input", inputs: []any{"https://example.com/repo.git", false, "module", "extra"}, wantErr: "invalid git.latest inputs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := updateGitLatestLockEntry(
				context.Background(),
				workspace.LookupEntry{Inputs: tc.inputs},
			)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestParseContainerFromLockInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		inputs    []any
		want      serverresolver.RegistryTransport
		wantError string
	}{
		{
			name:   "default transport",
			inputs: []any{"docker.io/library/alpine:latest"},
		},
		{
			name:   "plain HTTP",
			inputs: []any{"registry.example/acme/image:1.0.0", "http"},
			want: serverresolver.RegistryTransport{
				Protocol: serverresolver.RegistryProtocolHTTP,
			},
		},
		{
			name: "insecure HTTPS",
			inputs: []any{
				"registry.example/acme/image:1.0.0",
				"https",
				"insecureSkipTLSVerify",
			},
			want: serverresolver.RegistryTransport{
				Protocol:              serverresolver.RegistryProtocolHTTPS,
				InsecureSkipTLSVerify: true,
			},
		},
		{name: "missing inputs", wantError: "invalid container.from inputs"},
		{
			name:      "too many inputs",
			inputs:    []any{"alpine:latest", "https", "insecureSkipTLSVerify", "extra"},
			wantError: "invalid container.from inputs",
		},
		{name: "invalid ref", inputs: []any{42}, wantError: "invalid container.from ref"},
		{name: "empty ref", inputs: []any{""}, wantError: "invalid container.from ref"},
		{name: "invalid protocol type", inputs: []any{"alpine:latest", 42}, wantError: "invalid container.from registry protocol"},
		{name: "invalid protocol", inputs: []any{"alpine:latest", "ftp"}, wantError: "invalid container.from registry protocol"},
		{name: "invalid option", inputs: []any{"alpine:latest", "https", "other"}, wantError: "invalid container.from registry transport option"},
		{name: "insecure HTTP", inputs: []any{"alpine:latest", "http", "insecureSkipTLSVerify"}, wantError: "invalid container.from registry transport options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseContainerFromLockInputs(tc.inputs)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.inputs[0], got.ref)
			require.Equal(t, tc.inputs, got.lockInputs())
			require.Equal(t, tc.want, got.registryTransport)
		})
	}
}

func TestParseContainerFromLatestReleaseLockInputs(t *testing.T) {
	t.Parallel()

	inputs := []any{
		"docker.io/library/alpine",
		true,
		"https",
		"insecureSkipTLSVerify",
	}
	got, err := parseContainerFromLockInputs(inputs)
	require.NoError(t, err)
	require.True(t, got.latestRelease)
	require.True(t, got.latestIncludeSubreleases)
	require.Equal(t, inputs, got.lockInputs())
	require.Equal(t, serverresolver.RegistryTransport{
		Protocol:              serverresolver.RegistryProtocolHTTPS,
		InsecureSkipTLSVerify: true,
	}, got.registryTransport)

	_, err = parseContainerFromLockInputs(
		[]any{"docker.io/library/alpine", "false"},
	)
	require.ErrorContains(t, err, "invalid container.from latestIncludeSubreleases")

	tagged, err := parseContainerFromLockInputs(
		[]any{"docker.io/library/alpine:latest"},
	)
	require.NoError(t, err)
	require.False(t, tagged.latestRelease)

	_, err = parseContainerFromLockInputs(
		[]any{"docker.io/library/alpine:latest", false},
	)
	require.ErrorContains(t, err, "invalid container.from registry protocol")
}
