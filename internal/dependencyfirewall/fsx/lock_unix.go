//go:build !windows

package fsx

import (
	"os"
	"path/filepath"
	"syscall"
)

// WithLock runs fn while holding an exclusive advisory lock on a sidecar lock
// file at lockPath, creating the lock file (and its parent directory) if
// needed. The lock is released when fn returns.
//
// This serializes a read-modify-write sequence across processes. The
// Dependency Firewall's CI log is updated Load -> Append -> Save, and Save is
// a whole-file atomic rename; without a lock two concurrent "glab df run"
// invocations in one job race last-writer-wins, so the losing run's Blocked
// entries vanish and ci-summary exits 0. Holding this lock across the whole
// sequence makes the update atomic between processes.
//
// The lock is flock(2)-based advisory locking: it only coordinates callers
// that also take this lock (the firewall does, on the same sidecar path), not
// arbitrary writers. The sidecar is separate from the log itself so locking
// never conflicts with the atomic rename that replaces the log inode.
func WithLock(lockPath string, fn func() error) (err error) {
	if mkErr := os.MkdirAll(filepath.Dir(lockPath), 0o755); mkErr != nil {
		return mkErr
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		// Release then close. Report a close error only if fn succeeded, so a
		// real failure from fn is not masked by a cleanup error.
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if lerr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); lerr != nil {
		return lerr
	}
	return fn()
}
