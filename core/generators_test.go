package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleLoadFailureRegenerated(t *testing.T) {
	t.Parallel()

	changed := []string{".dagger/modules/stale/dagger.gen.go", "internal/dagger/client.go"}

	require.True(t, ModuleLoadFailure{Dir: ".dagger/modules/stale"}.Regenerated(changed))
	require.True(t, ModuleLoadFailure{Dir: ".dagger/modules/stale/"}.Regenerated(changed), "trailing slash")
	require.True(t, ModuleLoadFailure{Dir: "./.dagger/modules/stale"}.Regenerated(changed), "unclean dir")
	require.True(t, ModuleLoadFailure{Dir: "internal"}.Regenerated(changed), "nested change")
	require.True(t, ModuleLoadFailure{Dir: "."}.Regenerated(changed), "root module owns every path")

	require.False(t, ModuleLoadFailure{Dir: ".dagger/modules/stale-other"}.Regenerated(changed), "sibling prefix is not a parent")
	require.False(t, ModuleLoadFailure{Dir: ".dagger/modules/other"}.Regenerated(changed))
	require.False(t, ModuleLoadFailure{Dir: ""}.Regenerated(changed), "no directory (git source) is never regenerated")
	require.False(t, ModuleLoadFailure{Dir: "."}.Regenerated(nil), "no changes")
}

func TestModuleLoadOptionsAsModuleArgs(t *testing.T) {
	t.Parallel()

	opts := &ModuleLoadOptions{
		NameOverride:                "configured-name",
		LegacyDefaultPath:           true,
		DefaultPathContextSourceRef: "/workspace",
		DefaultPathContextSourcePin: "pin",
		WorkspaceConfigJSON:         `{"required":"value"}`,
		DefaultsFromDotEnv:          true,
		ArgCustomizationsJSON:       `[{"name":"required"}]`,
	}
	args := opts.AsModuleArgs()
	got := make([]string, 0, len(args))
	for _, arg := range args {
		got = append(got, arg.String())
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`legacyNameOverride: "configured-name"`,
		`legacyDefaultPath: true`,
		`defaultPathContextSourceRef: "/workspace"`,
		`defaultPathContextSourcePin: "pin"`,
		`legacyWorkspaceConfigJson: "{\"required\":\"value\"}"`,
		`legacyDefaultsFromDotEnv: true`,
		`legacyArgCustomizationsJson: "[{\"name\":\"required\"}]"`,
	} {
		require.Contains(t, joined, want)
	}
}
