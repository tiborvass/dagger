package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
)

// ErrNoGitContext reports that the module context has no git checkout at
// all: no .git entry at its root. Unlike a .git that exists but is unusable
// (which is a broken environment and fails the call), absence is a
// legitimate state -- `dagger init` before `git init`, an exported source
// tree -- so contextual GitRepository/GitRef args degrade to null on it.
var ErrNoGitContext = errors.New("module context has no git checkout")

// Checkouts created by `git submodule` and `git worktree` don't have a .git
// *directory* at the tree root: they have a .git pointer file (a "gitfile")
// whose `gitdir:` target lives outside the tree, typically inside the
// superproject's or main checkout's .git directory. Syncing such a tree gives
// the engine only the pointer file, so git operations against the snapshot
// fail with "not a git repository".
//
// resolveGitPointer reassembles such a context: it resolves the gitfile
// against the module's host context path, loads the real git directory (and
// the shared common directory, for worktrees) from the client, and flattens
// everything into a normal .git directory in the snapshot, so downstream git
// operations behave as if the context were a plain clone.
//
// Only local module sources are handled: they're the only kind with a client
// host to resolve the pointer against. A missing .git reports
// ErrNoGitContext (degradable); a .git that exists but can't be used as a
// git repository is a hard error.
func (src *ModuleSource) resolveGitPointer(
	ctx context.Context,
	dag *dagql.Server,
	dir dagql.ObjectResult[*Directory],
) (dagql.ObjectResult[*Directory], error) {
	st, err := dir.Self().Stat(ctx, dir, dag, ".git", true)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return dir, ErrNoGitContext
		}
		return dir, err
	}
	if st.FileType != FileTypeRegular {
		// A normal .git directory; nothing to resolve.
		return dir, nil
	}
	if src.Kind != ModuleSourceKindLocal {
		return dir, nil
	}

	pointer, err := readDirFile(ctx, dag, dir, ".git")
	if err != nil {
		return dir, fmt.Errorf("read .git pointer file: %w", err)
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(pointer)), "gitdir:")
	if !ok {
		return dir, notAGitRepo("malformed .git pointer file %q", strings.TrimSpace(string(pointer)))
	}
	// A relative gitdir is relative to the directory containing the pointer
	// file, i.e. the context root, which for a local source is a client host
	// path.
	hostGitDir := strings.TrimSpace(target)
	if !filepath.IsAbs(hostGitDir) {
		hostGitDir = filepath.Join(src.Local.ContextDirectoryPath, hostGitDir)
	}

	gitDir, err := src.loadClientDir(ctx, dag, hostGitDir)
	if err != nil {
		return dir, notAGitRepo("load .git pointer target %q: %s", hostGitDir, err)
	}
	if err := src.validateGitPointerTarget(ctx, dag, gitDir, hostGitDir); err != nil {
		return dir, err
	}

	// A worktree's git dir holds only per-worktree state (HEAD, index, ...)
	// plus a "commondir" pointer to the shared git dir; everything else
	// (objects, refs, config) lives there. A submodule's git dir is complete.
	// Flatten either shape into a single normal .git directory: shared state
	// as the base, per-worktree state layered on top.
	layers := []dagql.ObjectResult[*Directory]{gitDir}
	switch _, err := gitDir.Self().Stat(ctx, gitDir, dag, "commondir", false); {
	case err == nil:
		commonRel, err := readDirFile(ctx, dag, gitDir, "commondir")
		if err != nil {
			return dir, fmt.Errorf("read git commondir pointer: %w", err)
		}
		hostCommonDir := strings.TrimSpace(string(commonRel))
		if !filepath.IsAbs(hostCommonDir) {
			hostCommonDir = filepath.Join(hostGitDir, hostCommonDir)
		}
		commonDir, err := src.loadClientDir(ctx, dag, hostCommonDir)
		if err != nil {
			return dir, notAGitRepo("load git common dir %q: %s", hostCommonDir, err)
		}
		layers = []dagql.ObjectResult[*Directory]{commonDir, gitDir}
	case !errors.Is(err, fs.ErrNotExist):
		return dir, err
	}

	// The base layer must look like an actual git dir before we present it as
	// one. This also guards against a crafted pointer smuggling unrelated
	// host paths into the context as ".git".
	base := layers[0]
	for _, marker := range []string{"HEAD", "objects", "refs"} {
		if _, err := base.Self().Stat(ctx, base, dag, marker, false); err != nil {
			return dir, notAGitRepo(".git pointer target %q is not a git directory (missing %s)", hostGitDir, marker)
		}
	}

	sels := []dagql.Selector{{
		Field: "withoutFile",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(".git")},
		},
	}}
	for _, layer := range layers {
		layerID, err := layer.ID()
		if err != nil {
			return dir, fmt.Errorf("git dir layer ID: %w", err)
		}
		sels = append(sels, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(".git")},
				{Name: "source", Value: dagql.NewID[*Directory](layerID)},
			},
		})
	}
	// Drop worktree plumbing that only makes sense in the original layout:
	// other worktrees' registrations and this worktree's pointers back to the
	// host filesystem. All are no-ops for the submodule shape.
	sels = append(sels,
		dagql.Selector{
			Field: "withoutDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(".git/worktrees")},
			},
		},
		dagql.Selector{
			Field: "withoutFiles",
			Args: []dagql.NamedInput{
				{Name: "paths", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(
					".git/commondir", ".git/gitdir", ".git/locked",
				))},
			},
		},
	)

	var flattened dagql.ObjectResult[*Directory]
	if err := dag.Select(ctx, dir, &flattened, sels...); err != nil {
		return dir, fmt.Errorf("assemble flattened .git directory: %w", err)
	}

	// The copied config still describes the original layout: a submodule's
	// sets core.worktree to the old checkout path, and a bare main repo's
	// sets core.bare. Git honors the last definition of a single-valued key,
	// so append overrides describing the flattened shape instead of parsing
	// and rewriting the file. core.worktree is relative to the .git dir, so
	// ".." is the context root.
	cfg, err := readDirFile(ctx, dag, flattened, ".git/config")
	if err != nil {
		return dir, notAGitRepo(".git pointer target %q has no config: %s", hostGitDir, err)
	}
	const overrides = "\n# Appended by Dagger: the checkout was flattened from a submodule or\n# worktree layout into a standalone repository.\n[core]\n\tbare = false\n\tworktree = ..\n"
	if err := dag.Select(ctx, flattened, &flattened, dagql.Selector{
		Field: "withNewFile",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(".git/config")},
			{Name: "contents", Value: dagql.String(string(cfg) + overrides)},
		},
	}); err != nil {
		return dir, fmt.Errorf("override flattened git config: %w", err)
	}

	return flattened, nil
}

// loadClientDir loads an absolute path from the module's client host. Unlike
// contextual paths this is not bounded to the context directory: gitfile
// targets legitimately live outside it (e.g. the superproject's .git). The
// caller is responsible for validating what it loads.
func (src *ModuleSource) loadClientDir(
	ctx context.Context,
	dag *dagql.Server,
	hostPath string,
) (inst dagql.ObjectResult[*Directory], err error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	md, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}
	clientCtx := engine.ContextWithClientMetadata(ctx, md)
	err = dag.Select(clientCtx, dag.Root(), &inst,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(hostPath)},
				{Name: "noCache", Value: dagql.Boolean(true)},
			},
		},
	)
	return inst, err
}

func readDirFile(
	ctx context.Context,
	dag *dagql.Server,
	dir dagql.ObjectResult[*Directory],
	path string,
) ([]byte, error) {
	var f dagql.ObjectResult[*File]
	if err := dag.Select(ctx, dir, &f, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(path)},
		},
	}); err != nil {
		return nil, err
	}
	return f.Self().Contents(ctx, f, nil, nil)
}

func (src *ModuleSource) validateGitPointerTarget(
	ctx context.Context,
	dag *dagql.Server,
	gitDir dagql.ObjectResult[*Directory],
	hostGitDir string,
) error {
	contextDir := cleanHostPath(src.Local.ContextDirectoryPath)
	contextGitFile := cleanHostPath(filepath.Join(contextDir, ".git"))

	// Linked worktree git dirs carry a gitdir file pointing back to the
	// worktree's .git pointer file.
	switch _, err := gitDir.Self().Stat(ctx, gitDir, dag, "gitdir", false); {
	case err == nil:
		backref, err := readDirFile(ctx, dag, gitDir, "gitdir")
		if err != nil {
			return fmt.Errorf("read gitdir back-reference: %w", err)
		}
		backrefPath := resolveHostGitPath(hostGitDir, string(backref))
		if !sameHostPath(backrefPath, contextGitFile) {
			return notAGitRepo(".git pointer target %q points back to %q, not context gitfile %q",
				hostGitDir, backrefPath, contextGitFile)
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}

	// Submodule git dirs carry their back-reference as core.worktree in config.
	cfg, err := readDirFile(ctx, dag, gitDir, "config")
	if err != nil {
		return notAGitRepo(".git pointer target %q has no config back-reference to context %q: %s",
			hostGitDir, contextDir, err)
	}
	worktree, ok := gitConfigValue(cfg, "core", "worktree")
	if !ok {
		return notAGitRepo(".git pointer target %q does not point back to context %q",
			hostGitDir, contextDir)
	}
	worktreePath := resolveHostGitPath(hostGitDir, worktree)
	if !sameHostPath(worktreePath, contextDir) {
		return notAGitRepo(".git pointer target %q points back to %q, not context %q",
			hostGitDir, worktreePath, contextDir)
	}
	return nil
}

func resolveHostGitPath(base, p string) string {
	p = strings.TrimSpace(p)
	if filepath.IsAbs(p) {
		return cleanHostPath(p)
	}
	return cleanHostPath(filepath.Join(base, p))
}

func sameHostPath(a, b string) bool {
	return cleanHostPath(a) == cleanHostPath(b)
}

func cleanHostPath(p string) string {
	return filepath.Clean(strings.TrimSpace(p))
}

func gitConfigValue(cfg []byte, section, key string) (string, bool) {
	var (
		inSection bool
		value     string
		found     bool
	)
	section = strings.ToLower(section)
	key = strings.ToLower(key)
	for line := range strings.Lines(string(cfg)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			name := strings.TrimSpace(line[1:strings.Index(line, "]")])
			inSection = strings.EqualFold(name, section)
			continue
		}
		if !inSection {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), key) {
			continue
		}
		value = strings.TrimSpace(v)
		found = true
	}
	return value, found
}

func notAGitRepo(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, gitutil.ErrGitNoRepo)...)
}
