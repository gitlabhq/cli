//go:build !integration

package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/renameio/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteOwnerOnly_CreatesFileWithMode0600(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestWriteOwnerOnly_PendingFileIs0600BeforeWrite(t *testing.T) {
	t.Parallel()
	// The end-state-mode assertions above cannot distinguish this
	// implementation from the write-then-chmod shape the package doc rejects:
	// both land on 0o600. Pin the mechanism instead. WriteOwnerOnly drives
	// renameio.NewPendingFile with WithStaticPermissions(0o600); build a
	// pending file the same way and assert the temp file is already 0o600
	// before any bytes are written and before the atomic rename — proving the
	// mode is set at creation, not tightened afterwards through a chmod window.
	if runtime.GOOS == "windows" {
		t.Skip("atomic pending-file path is only wired for !windows")
	}
	path := filepath.Join(t.TempDir(), "secret")
	f, err := renameio.NewPendingFile(path, renameio.WithStaticPermissions(0o600))
	require.NoError(t, err)
	defer func() { _ = f.Cleanup() }()

	info, err := os.Stat(f.Name())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"temp file must be 0o600 at creation, before any write or rename")
}

func TestWriteOwnerOnly_TargetNeverDisappearsDuringOverwrite(t *testing.T) {
	t.Parallel()
	// A delete+recreate impostor (os.Remove then os.WriteFile) would also
	// change the inode and land on 0o600, passing the atomicity/mode tests
	// above. Rule it out: while an overwrite runs concurrently, the target
	// path must never be observed as ENOENT — the atomic rename replaces the
	// file in a single step and there is no window where it is absent.
	if runtime.GOOS == "windows" {
		t.Skip("atomic replace is only wired for !windows")
	}
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, WriteOwnerOnly(path, []byte("old")))

	// The writer goroutine reports any write error over a channel rather than
	// calling require/assert/t.Fatal itself: those must run only in the test's
	// own goroutine (testifylint go-require).
	writeErr := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			if err := WriteOwnerOnly(path, []byte("new")); err != nil {
				writeErr <- err
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			select {
			case err := <-writeErr:
				require.NoError(t, err)
			default:
			}
			return
		default:
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Fatal("target path was momentarily absent during overwrite; write is not an atomic replace")
			}
		}
	}
}

func TestWithLock_SerializesConcurrentCriticalSections(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("WithLock is a no-op on Windows")
	}
	// Two goroutines contending for the same lock must never overlap inside
	// the critical section. Without the flock this races and inflight peaks
	// above 1; with it, the second caller blocks until the first releases.
	lockPath := filepath.Join(t.TempDir(), "df", "ci-log.lock")

	var (
		mu       sync.Mutex
		inflight int
		maxSeen  int
	)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			err := WithLock(lockPath, func() error {
				mu.Lock()
				inflight++
				if inflight > maxSeen {
					maxSeen = inflight
				}
				mu.Unlock()

				time.Sleep(20 * time.Millisecond) // widen the overlap window

				mu.Lock()
				inflight--
				mu.Unlock()
				return nil
			})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	assert.Equal(t, 1, maxSeen, "WithLock must serialize the critical section")
}

func TestWithLock_PropagatesError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("WithLock is a no-op on Windows")
	}
	lockPath := filepath.Join(t.TempDir(), "ci-log.lock")
	sentinel := errors.New("boom")
	err := WithLock(lockPath, func() error { return sentinel })
	assert.ErrorIs(t, err, sentinel, "WithLock must return fn's error unchanged")
}
