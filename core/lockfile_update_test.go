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

func TestLatestDependencyEntry(t *testing.T) {
	t.Parallel()

	t.Run("Git", func(t *testing.T) {
		t.Parallel()

		entry, err := latestDependencyEntry(
			workspace.LookupEntry{
				Operation: lockGitLatestOperation,
				Inputs: workspace.LookupInputs(
					[]any{"https://example.com/repo.git"},
					workspace.LookupOption{Name: "tagPrefix", Value: "sdk/go"},
				),
			},
			workspace.LookupResult{Value: "refs/tags/sdk/go/v1.2.3"},
		)
		require.NoError(t, err)
		require.Equal(t, lockGitSHAOperation, entry.Operation)
		require.Equal(t, []any{
			"https://example.com/repo.git",
			"refs/tags/sdk/go/v1.2.3",
		}, entry.Inputs)
	})

	t.Run("OCI", func(t *testing.T) {
		t.Parallel()

		entry, err := latestDependencyEntry(
			workspace.LookupEntry{
				Operation: lockOCILatestOperation,
				Inputs: workspace.LookupInputs(
					[]any{"registry.example/acme/image"},
					workspace.LookupOption{Name: "includePrereleases", Value: true},
					workspace.LookupOption{Name: "protocol", Value: "http"},
				),
			},
			workspace.LookupResult{Value: "2.0.0-rc.1"},
		)
		require.NoError(t, err)
		require.Equal(t, lockOCISHAOperation, entry.Operation)
		require.Equal(t, workspace.LookupInputs(
			[]any{"registry.example/acme/image:2.0.0-rc.1"},
			workspace.LookupOption{Name: "protocol", Value: "http"},
		), entry.Inputs)
	})
}

func TestUpdateGitLatestLockEntryValidatesInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		inputs  []any
		wantErr string
	}{
		{name: "missing inputs", wantErr: "invalid git-latest inputs"},
		{name: "invalid remote type", inputs: []any{42}, wantErr: "invalid git-latest remote"},
		{name: "empty remote", inputs: []any{""}, wantErr: "invalid git-latest remote"},
		{
			name: "invalid prerelease policy",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "includePrereleases", Value: "false"},
			),
			wantErr: "invalid git-latest includePrereleases",
		},
		{
			name: "invalid prefix type",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "tagPrefix", Value: 42},
			),
			wantErr: "invalid git-latest tagPrefix",
		},
		{
			name: "empty prefix",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "tagPrefix", Value: ""},
			),
			wantErr: "invalid git-latest tagPrefix",
		},
		{
			name: "unknown option",
			inputs: workspace.LookupInputs(
				[]any{"https://example.com/repo.git"},
				workspace.LookupOption{Name: "channel", Value: "beta"},
			),
			wantErr: "invalid git-latest option",
		},
		{
			name:    "extra input",
			inputs:  []any{"https://example.com/repo.git", "extra"},
			wantErr: "invalid git-latest inputs",
		},
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

func TestParseOCILockInputs(t *testing.T) {
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
			name: "plain HTTP",
			inputs: workspace.LookupInputs(
				[]any{"registry.example/acme/image:1.0.0"},
				workspace.LookupOption{Name: "protocol", Value: "http"},
			),
			want: serverresolver.RegistryTransport{
				Protocol: serverresolver.RegistryProtocolHTTP,
			},
		},
		{
			name: "insecure HTTPS",
			inputs: workspace.LookupInputs(
				[]any{"registry.example/acme/image:1.0.0"},
				workspace.LookupOption{Name: "protocol", Value: "https"},
				workspace.LookupOption{Name: "insecureSkipTLSVerify", Value: true},
			),
			want: serverresolver.RegistryTransport{
				Protocol:              serverresolver.RegistryProtocolHTTPS,
				InsecureSkipTLSVerify: true,
			},
		},
		{name: "missing inputs", wantError: "invalid oci-sha inputs"},
		{
			name:      "too many inputs",
			inputs:    []any{"alpine:latest", "extra"},
			wantError: "invalid oci-sha inputs",
		},
		{name: "invalid ref", inputs: []any{42}, wantError: "invalid oci-sha ref"},
		{name: "empty ref", inputs: []any{""}, wantError: "invalid oci-sha ref"},
		{name: "untagged ref", inputs: []any{"alpine"}, wantError: "invalid oci-sha untagged ref"},
		{
			name: "invalid protocol",
			inputs: workspace.LookupInputs(
				[]any{"alpine:latest"},
				workspace.LookupOption{Name: "protocol", Value: "ftp"},
			),
			wantError: "invalid oci-sha registry protocol",
		},
		{
			name: "unknown option",
			inputs: workspace.LookupInputs(
				[]any{"alpine:latest"},
				workspace.LookupOption{Name: "other", Value: true},
			),
			wantError: "invalid oci-sha option",
		},
		{
			name: "insecure HTTP",
			inputs: workspace.LookupInputs(
				[]any{"alpine:latest"},
				workspace.LookupOption{Name: "protocol", Value: "http"},
				workspace.LookupOption{Name: "insecureSkipTLSVerify", Value: true},
			),
			wantError: "invalid oci-sha registry transport options",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseOCILockInputs(lockOCISHAOperation, tc.inputs, false)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.inputs[0], got.ref)
			require.Equal(t, tc.want, got.registryTransport)
		})
	}
}

func TestParseOCILatestLockInputs(t *testing.T) {
	t.Parallel()

	inputs := workspace.LookupInputs(
		[]any{"docker.io/library/alpine"},
		workspace.LookupOption{Name: "includePrereleases", Value: true},
		workspace.LookupOption{Name: "protocol", Value: "https"},
		workspace.LookupOption{Name: "insecureSkipTLSVerify", Value: true},
	)
	got, err := parseOCILockInputs(lockOCILatestOperation, inputs, true)
	require.NoError(t, err)
	require.True(t, got.includePrereleases)
	require.Equal(t, serverresolver.RegistryTransport{
		Protocol:              serverresolver.RegistryProtocolHTTPS,
		InsecureSkipTLSVerify: true,
	}, got.registryTransport)

	_, err = parseOCILockInputs(
		lockOCILatestOperation,
		[]any{"docker.io/library/alpine:latest"},
		true,
	)
	require.ErrorContains(t, err, "invalid oci-latest tagged ref")

	_, err = parseOCILockInputs(
		lockOCILatestOperation,
		workspace.LookupInputs(
			[]any{"docker.io/library/alpine"},
			workspace.LookupOption{Name: "includePrereleases", Value: "false"},
		),
		true,
	)
	require.ErrorContains(t, err, "invalid oci-latest includePrereleases")

	_, err = parseOCILockInputs(
		lockOCISHAOperation,
		workspace.LookupInputs(
			[]any{"docker.io/library/alpine:latest"},
			workspace.LookupOption{Name: "includePrereleases", Value: false},
		),
		false,
	)
	require.ErrorContains(t, err, `invalid oci-sha option "includePrereleases"`)
}
