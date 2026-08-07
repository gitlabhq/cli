//go:build !integration

package fsx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteOwnerOnly_CreatesFileWithMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "secret")

	require.NoError(t, WriteOwnerOnly(path, []byte("token")))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "token", string(got))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteOwnerOnly_TightensExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	require.NoError(t, WriteOwnerOnly(path, []byte("new")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteOwnerOnly_IsAtomicOverwrite(t *testing.T) {
	// Atomicity is not directly observable, but renameio's temp-file-plus-rename
	// implementation changes the file's inode on every write. os.WriteFile, in
	// contrast, truncates and rewrites the same inode. Assert the inode changes
	// on overwrite as a proxy for "the new bytes appeared via rename, not via
	// in-place truncation" — which is the observable property that guarantees
	// a reader either sees the full old file or the full new file, never a
	// half-written mix.
	if runtime.GOOS == "windows" {
		t.Skip("inode identity is a POSIX concept; atomicity path is only wired for !windows")
	}
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, WriteOwnerOnly(path, []byte("old")))

	before, err := os.Stat(path)
	require.NoError(t, err)
	beforeStat, ok := before.Sys().(*syscall.Stat_t)
	require.True(t, ok, "expected *syscall.Stat_t on POSIX")

	require.NoError(t, WriteOwnerOnly(path, []byte("new")))

	after, err := os.Stat(path)
	require.NoError(t, err)
	afterStat, ok := after.Sys().(*syscall.Stat_t)
	require.True(t, ok, "expected *syscall.Stat_t on POSIX")

	assert.NotEqual(t, beforeStat.Ino, afterStat.Ino, "expected atomic replace (new inode) rather than in-place truncation")
}

func TestWriteExecutable_CreatesFileWithMode0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "shim")

	require.NoError(t, WriteExecutable(path, []byte("script")))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "script", string(got))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// TestWriteExecutable_ForcesModeRegardlessOfExistingFile pins that the mode
// is not copied from a pre-existing file the way WithExistingPermissions
// would: a shim left at a stale mode by an older version must still come
// back executable.
func TestWriteExecutable_ForcesModeRegardlessOfExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "shim")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	require.NoError(t, WriteExecutable(path, []byte("new")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// TestWriteExecutable_IsAtomicOverwrite is the same inode-identity proxy
// TestWriteOwnerOnly_IsAtomicOverwrite uses: the file only ever appears at
// its final path via rename, never via in-place truncation, so a concurrent
// reader cannot observe a torn or transiently non-executable file.
func TestWriteExecutable_IsAtomicOverwrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inode identity is a POSIX concept; atomicity path is only wired for !windows")
	}
	path := filepath.Join(t.TempDir(), "shim")
	require.NoError(t, WriteExecutable(path, []byte("old")))

	before, err := os.Stat(path)
	require.NoError(t, err)
	beforeStat, ok := before.Sys().(*syscall.Stat_t)
	require.True(t, ok, "expected *syscall.Stat_t on POSIX")

	require.NoError(t, WriteExecutable(path, []byte("new")))

	after, err := os.Stat(path)
	require.NoError(t, err)
	afterStat, ok := after.Sys().(*syscall.Stat_t)
	require.True(t, ok, "expected *syscall.Stat_t on POSIX")

	assert.NotEqual(t, beforeStat.Ino, afterStat.Ino, "expected atomic replace (new inode) rather than in-place truncation")
}

func TestWriteJSONFile_CreatesParentAndAppendsNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "config.json")

	require.NoError(t, WriteJSONFile(path, map[string]any{"k": "v"}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	// Two-space indent is deliberate (users hand-edit this file), and
	// the trailing newline is a POSIX text-file convention that keeps
	// `git diff` clean on later edits — assert both explicitly rather
	// than with JSONEq, which strips whitespace.
	body := string(raw)
	assert.True(t, strings.HasSuffix(body, "\n"), "file should end with newline, got %q", body)
	assert.Contains(t, body, "  \"k\": \"v\"", "expected two-space indent")
}
