//go:build windows

package config

import (
	"os"

	"golang.org/x/sys/windows"
)

// tryFlock attempts a non-blocking exclusive lock on f. A false result with a
// nil error means another process holds the lock.
func tryFlock(f *os.File) (bool, error) {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped),
	)
	if err == nil {
		return true, nil
	}
	if err == windows.ERROR_LOCK_VIOLATION || err == windows.ERROR_IO_PENDING {
		return false, nil
	}
	return false, err
}

// funlock releases a lock held on f. The lock is also released when f is closed
// or the process exits.
func funlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
