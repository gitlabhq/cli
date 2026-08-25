package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

const (
	// lockFilename is the advisory lock file that serializes credential writes
	// across concurrent glab processes. It sits beside config.yml and is never
	// itself rewritten, so it is unaffected by renameio's atomic replace of
	// config.yml.
	lockFilename = "config.lock"
	// lockPollInterval is how often a blocked acquire retries the lock.
	lockPollInterval = 20 * time.Millisecond
)

// lockTimeout bounds how long acquireLock waits for another process to release
// the lock before giving up, so a wedged holder cannot block a command forever.
// It is a var so tests can shorten it.
var lockTimeout = 3 * time.Second

// errLockTimeout signals that the lock could not be acquired within lockTimeout.
var errLockTimeout = errors.New("timed out waiting for config lock")

// fileLock is a cross-process advisory lock scoped to a config directory. The OS
// releases it automatically when the process exits, so a crashed holder never
// leaves a stale lock behind (no manual staleness detection required).
type fileLock struct {
	f *os.File
}

// acquireLock takes an exclusive cross-process lock for the config directory,
// polling up to lockTimeout. An empty dir (in-memory config) returns a nil lock
// and no error. On timeout it returns errLockTimeout so the caller can decide to
// proceed unlocked rather than fail.
func acquireLock(dir string) (*fileLock, error) {
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(filepath.Join(dir, lockFilename), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(lockTimeout)
	for {
		locked, err := tryFlock(f)
		if err != nil {
			return nil, errors.Join(err, f.Close())
		}
		if locked {
			return &fileLock{f: f}, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.Join(errLockTimeout, f.Close())
		}
		time.Sleep(lockPollInterval)
	}
}

// release unlocks and closes the lock file. A nil lock (in-memory config) is a
// no-op.
func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	funlock(l.f)
	_ = l.f.Close()
}

// withLock runs fn while holding the config-directory lock. If the lock cannot
// be acquired within the timeout, fn still runs (unlocked) so a wedged peer
// never blocks a command; the degraded path is logged for GLAB_DEBUG.
func withLock(dir string, fn func() error) error {
	lock, err := acquireLock(dir)
	if err != nil {
		dbg.Debugf("config: proceeding without lock for %q: %v", dir, err)
		return fn()
	}
	defer lock.release()
	return fn()
}
