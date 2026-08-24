package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalLockFilePath(t *testing.T) {
	require.Equal(t, "dagger.lock", CanonicalLockFilePath(filepath.Join(".dagger", "lock")))
	require.Equal(t, filepath.Join("app", "dagger.lock"), CanonicalLockFilePath(filepath.Join("app", ".dagger", "lock")))
	require.Equal(t, filepath.Join("app", "dagger.lock"), CanonicalLockFilePath(filepath.Join("app", "dagger.lock")))
	require.Equal(t, filepath.Join("app", "lock"), CanonicalLockFilePath(filepath.Join("app", "lock")))
}

func TestLookupSetGetDelete(t *testing.T) {
	lock := NewLock()
	inputs := []any{"alpine:latest", "linux/amd64"}

	require.NoError(t, lock.SetLookup("", "oci-sha", inputs, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))

	result, ok, err := lock.GetLookup("", "oci-sha", inputs)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "sha256:deadbeef", result.Value)
	require.Equal(t, PolicyPin, result.Policy)

	require.True(t, lock.DeleteLookup("", "oci-sha", inputs))
	_, ok, err = lock.GetLookup("", "oci-sha", inputs)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestLookupSetValidation(t *testing.T) {
	lock := NewLock()

	err := lock.SetLookup("", "oci-sha", []any{"alpine:latest"}, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: LockPolicy("weird"),
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid lock policy")
}

func TestLookupConcurrentWrites(t *testing.T) {
	t.Parallel()

	lock := NewLock()
	const writes = 100
	errs := make(chan error, writes)
	var wg sync.WaitGroup
	for i := range writes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- lock.SetLookup("", "git-sha", []any{"repo", fmt.Sprint(i)}, LookupResult{
				Value:  fmt.Sprintf("%040d", i),
				Policy: PolicyPin,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	entries, err := lock.Entries()
	require.NoError(t, err)
	require.Len(t, entries, writes)
}

func TestParseLockRejectsV1(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","container.from",["alpine:latest","linux/amd64"],"sha256:deadbeef","weird"]`,
	}, "\n")

	_, err := ParseLock([]byte(input))
	require.ErrorContains(t, err, `unsupported lockfile version "1"`)
}

func TestEntries(t *testing.T) {
	lock := NewLock()
	inputs := []any{"alpine:latest", "linux/amd64"}

	require.NoError(t, lock.SetLookup("", "oci-sha", inputs, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))

	entries, err := lock.Entries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, LookupEntry{
		Namespace: "",
		Operation: "oci-sha",
		Inputs:    inputs,
		Result: LookupResult{
			Value:  "sha256:deadbeef",
			Policy: PolicyPin,
		},
	}, entries[0])
}

func TestClone(t *testing.T) {
	lock := NewLock()
	require.NoError(t, lock.SetLookup("", "oci-sha", []any{"alpine:latest"}, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))

	cloned, err := lock.Clone()
	require.NoError(t, err)

	require.NoError(t, cloned.SetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"}, LookupResult{
		Value:  "0123456789abcdef0123456789abcdef01234567",
		Policy: PolicyPin,
	}))

	_, ok, err := lock.GetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"})
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMerge(t *testing.T) {
	base := NewLock()
	require.NoError(t, base.SetLookup("", "oci-sha", []any{"alpine:latest"}, LookupResult{
		Value:  "sha256:deadbeef",
		Policy: PolicyPin,
	}))

	delta := NewLock()
	require.NoError(t, delta.SetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"}, LookupResult{
		Value:  "0123456789abcdef0123456789abcdef01234567",
		Policy: PolicyPin,
	}))

	require.NoError(t, base.Merge(delta))

	result, ok, err := base.GetLookup("", "oci-sha", []any{"alpine:latest"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, LookupResult{Value: "sha256:deadbeef", Policy: PolicyPin}, result)

	result, ok, err = base.GetLookup("", "git.branch", []any{"https://github.com/dagger/dagger.git", "main"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, LookupResult{Value: "0123456789abcdef0123456789abcdef01234567", Policy: PolicyPin}, result)
}
