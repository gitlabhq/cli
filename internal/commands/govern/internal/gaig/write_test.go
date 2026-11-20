//go:build !integration

package gaig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	require.NoError(t, os.WriteFile(path, []byte("old content that is longer"), 0o644))

	require.NoError(t, WriteFileAtomic(path, []byte("new")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))
	assertOnlyFiles(t, dir, "state.json")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestWriteFileAtomic_FailedRenameLeavesNoTempFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A non-empty directory cannot be replaced by a file.
	path := filepath.Join(dir, "state")
	require.NoError(t, os.MkdirAll(filepath.Join(path, "child"), 0o755))

	require.Error(t, WriteFileAtomic(path, []byte("new")))

	assertOnlyFiles(t, dir, "state")
}

func assertOnlyFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	assert.ElementsMatch(t, names, got, "no temporary file is left behind")
}
