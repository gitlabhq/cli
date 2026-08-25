//go:build !windows

package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryFlock attempts a non-blocking exclusive lock on f. A false result with a
// nil error means another process holds the lock.
func tryFlock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == unix.EWOULDBLOCK {
		return false, nil
	}
	return false, err
}

// funlock releases a lock held on f. The lock is also released when f is closed
// or the process exits.
func funlock(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
