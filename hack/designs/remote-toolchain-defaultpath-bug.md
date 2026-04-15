# `dagger -m <remote>` from a non-workspace CWD

## The bug

```
cd /tmp/empty          # any directory without .git or dagger.json up the tree
dagger -m github.com/dagger/dagger@main check java-sdk:lint
# → fails: workspace.directory("sdk/java/runtime/images/maven") doesn't exist
```

The same command from a clone of `dagger/dagger` succeeds.

The toolchain's `workspace` argument is declared with
`@defaultPath(path: "/") @ignorePatterns([...])`. Under the current
engine behaviour, that argument is resolved against the CLI's host CWD.
If CWD isn't a workspace, there's nothing there — and any file the
toolchain tries to read via `workspace.directory(...)` is gone.

## Why it happens

`-m` loads a module, but the **workspace** is picked separately by
`ensureWorkspaceLoaded` (`engine/server/session_workspaces.go`):

- `workspaceBindingMode` returns `DetectHost` for a plain
  `dagger -m …` invocation.
- `loadWorkspaceFromHost` runs `workspace.Detect(cwd)`, which falls back
  to the CWD itself when no `.git` is found going up.
- That becomes the workspace the session uses for
  `currentWorkspace.directory(...)` resolution.

Most toolchains today are loaded through the `LegacyDefaultPath=true`
path (workspace-attached toolchains, see `b35e37b0e`), which resolves
their `defaultPath="/"` workspace arg via `currentWorkspace.directory()`
— i.e. against that host CWD. When the host CWD is an empty/unrelated
directory, the resolved workspace directory is empty, and subsequent
`workspace.directory("sdk/java/…")` calls fail.

## The fix

`engine/server/session_workspaces.go: loadWorkspaceFromHostPath`
auto-promotes the `-m` remote ref to the workspace when:

1. there is exactly one remote (git-kind) entrypoint extra module, and
2. the CWD has no workspace markers (no `.git` and no `dagger.json`
   going up).

Under those conditions, the function delegates to
`loadWorkspaceFromRemote` with the remote ref — the same path an
explicitly declared remote workspace binding already takes. If CWD *is*
a workspace, behaviour is unchanged (local workspace wins, the CLI
still feels like it did before).

Two small helpers accompany the change:

- `autoRemoteWorkspaceRefFromExtras` — picks the single entrypoint
  remote ref, returns `false` on ambiguity (multiple entrypoints) or
  purely local `-m`. Uses `ParseRefString` rather than
  `FastModuleSourceKindCheck` so the common `github.com/…` shape is
  correctly classified (fast-check defers on dotted hosts).
- `cwdHasWorkspaceMarkers` — walks up from CWD looking for `.git` or
  `dagger.json`. Same two signals the existing workspace detection
  relies on.

## What `c41d6707d` / `b35e37b0e` have to do with it

Neither commit introduced the bug — it was always latent. Before them,
the session load path typically reached the host workspace through a
different route. `c41d6707d` (opt-in workspace module loading) and
`b35e37b0e` (preserve legacy toolchain customizations for explicit
`-m` modules) narrowed the situations in which the legacy route fires,
exposing the gap when CWD isn't a workspace. The fix complements them
rather than reverting either.

## Test coverage

`TestModule.TestRemoteWorkspaceToolchainDefaultPath` in
`core/integration/module_remote_workspace_defaultpath_test.go` exercises
both conditions against the fixture at
`github.com/dagger/dagger-test-modules`, branch `workspace-default-path`:

| Subtest | CWD | Expected |
|---|---|---|
| `empty cwd` | alpine, no `.git`, no `dagger.json` | must pass once the fix lands — target regression |
| `workspace cwd` | goGitBase (`/work` with `.git`), fixture files layered in | passes today and after — non-regression guard |

Both subtests invoke `dagger -m <fixture-root>@<sha> check greeter:read-check`
— the same shape as the reported
`dagger -m github.com/dagger/dagger@main check java-sdk:lint`.

The fixture branch declares `greeter` as a toolchain in its root
`dagger.json`, and `greeter` is a Go module with a constructor-injected
`workspace` arg annotated `+defaultPath="/" +ignore=["*",
"!workspace-default-path/target-subdir/"]` that reads
`workspace.Directory("workspace-default-path/target-subdir/maven").File("hello.txt")`.
That mirrors `toolchains/java-sdk-dev` exactly: a workspace-attached
toolchain whose resolved workspace must come from the remote repo
when the CLI is invoked from outside any local workspace.
