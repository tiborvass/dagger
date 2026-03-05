package core

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestRemoteFromCacheResultAcceptsStringPayload(t *testing.T) {
	payloadRemote := &gitutil.Remote{
		Refs: []*gitutil.Ref{
			{Name: "refs/heads/main", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		Symrefs: map[string]string{
			"HEAD": "refs/heads/main",
		},
	}
	payload, err := json.Marshal(payloadRemote)
	require.NoError(t, err)

	remote, err := remoteFromCacheResult(string(payload))
	require.NoError(t, err)
	require.Len(t, remote.Refs, 1)
	require.Equal(t, payloadRemote.Refs[0].Name, remote.Refs[0].Name)
	require.Equal(t, payloadRemote.Refs[0].SHA, remote.Refs[0].SHA)
	require.Equal(t, payloadRemote.Symrefs["HEAD"], remote.Symrefs["HEAD"])
}

func TestRemoteFromCacheResultRejectsInvalidPayload(t *testing.T) {
	_, err := remoteFromCacheResult("{not-json")
	require.ErrorContains(t, err, "decode cached remote")
}

func TestRemoteMetadataCacheKeyIsolation(t *testing.T) {
	ctx := context.Background()

	cacheIface, err := dagql.NewCache(ctx, "")
	require.NoError(t, err)

	remotePayload := `{"refs":[]}`
	_, err = cacheIface.GetOrInitArbitrary(ctx, "git-remote-test-dedicated-key", dagql.ArbitraryValueFunc(remotePayload))
	require.NoError(t, err)

	gitInitCalls := 0
	gitPayload := `{"refs":[{"name":"refs/heads/main","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`
	res, err := cacheIface.GetOrInitArbitrary(ctx, "git-current-call-key", func(context.Context) (any, error) {
		gitInitCalls++
		return gitPayload, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, gitInitCalls, "unrelated call key should not be aliased to remote metadata payload")
	require.Equal(t, gitPayload, res.Value())
}

func TestNamedFetchRefSpecs(t *testing.T) {
	commitSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	refs := []*RemoteGitRef{
		{
			Ref: &gitutil.Ref{
				Name: "refs/heads/main",
				SHA:  commitSHA,
			},
		},
		{
			Ref: &gitutil.Ref{
				Name: "refs/tags/v1.0.0",
				SHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
		{
			Ref: &gitutil.Ref{
				SHA: "cccccccccccccccccccccccccccccccccccccccc",
			},
		},
		{
			Ref: &gitutil.Ref{
				Name: commitSHA,
				SHA:  commitSHA,
			},
		},
		nil,
	}

	specs := namedFetchRefSpecs(refs)
	require.Len(t, specs, 2, "only named, non-commit refs should generate fallback specs")
	require.Equal(t, namedFetchRefSpecs(refs), specs, "fallback specs should be deterministic")

	for _, spec := range specs {
		src, dst, ok := strings.Cut(spec, ":")
		require.True(t, ok)
		require.NotEmpty(t, src)
		require.True(t, strings.HasPrefix(dst, "refs/dagger.fetch/"))
		require.NotContains(t, dst, ":", "fallback destination ref should be a valid git ref component")
	}
}

func TestNamedFetchRefSpecsChangesWithPinnedSHA(t *testing.T) {
	base := []*RemoteGitRef{
		{
			Ref: &gitutil.Ref{
				Name: "refs/heads/main",
				SHA:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	}
	updated := []*RemoteGitRef{
		{
			Ref: &gitutil.Ref{
				Name: "refs/heads/main",
				SHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	}

	baseSpecs := namedFetchRefSpecs(base)
	updatedSpecs := namedFetchRefSpecs(updated)
	require.Len(t, baseSpecs, 1)
	require.Len(t, updatedSpecs, 1)
	require.NotEqual(t, baseSpecs[0], updatedSpecs[0], "fallback destination should track pinned ref+sha pair")
}

func TestMirrorGitRemoteToBundleSupportsLsRemoteAndFetch(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")

	runGitCmd(t, tmpDir, "init", "--initial-branch=main", srcDir)
	runGitCmd(t, srcDir, "config", "user.email", "test@dagger.io")
	runGitCmd(t, srcDir, "config", "user.name", "Test User")
	runGitCmd(t, srcDir, "commit", "--allow-empty", "-m", "init")
	runGitCmd(t, srcDir, "branch", "feature")
	runGitCmd(t, srcDir, "tag", "v1.0.0")

	commitSHA := strings.TrimSpace(runGitCmd(t, srcDir, "rev-parse", "HEAD"))
	bundlePath := filepath.Join(tmpDir, "git-bundles", "repo.bundle")

	err := mirrorGitRemoteToBundle(ctx, gitutil.NewGitCLI(), srcDir, bundlePath)
	require.NoError(t, err)

	remote, err := gitutil.NewGitCLI().LsRemote(ctx, bundlePath)
	require.NoError(t, err)
	require.NotNil(t, remote.Get("HEAD"))
	require.NotNil(t, remote.Get("refs/heads/main"))
	require.NotNil(t, remote.Get("refs/heads/feature"))
	require.NotNil(t, remote.Get("refs/tags/v1.0.0"))
	require.Equal(t, commitSHA, remote.Get("HEAD").SHA)
	require.Equal(t, commitSHA, remote.Get("refs/heads/main").SHA)
	require.Equal(t, commitSHA, remote.Get("refs/heads/feature").SHA)
	require.Equal(t, commitSHA, remote.Get("refs/tags/v1.0.0").SHA)

	dstDir := filepath.Join(tmpDir, "dst.git")
	runGitCmd(t, tmpDir, "init", "--bare", dstDir)

	dstGit := gitutil.NewGitCLI(gitutil.WithGitDir(dstDir))
	_, err = dstGit.Run(ctx, "fetch", bundlePath, commitSHA)
	require.NoError(t, err)

	fetchedSHA, err := dstGit.Run(ctx, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	require.NoError(t, err)
	require.Equal(t, commitSHA, strings.TrimSpace(string(fetchedSHA)))
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), out)
	return string(out)
}
