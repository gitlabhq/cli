//go:build windows

package fsx

// WithLock runs fn with no cross-process locking on Windows.
//
// The unix implementation uses flock(2); there is no drop-in equivalent here
// without a syscall/LockFileEx wrapper. The dependency-firewall engine core
// targets Linux CI runners, where concurrent "glab df run" invocations in one
// job actually race and the flock guarantee holds. On Windows the sequence
// runs unlocked, matching how WriteOwnerOnly degrades its atomicity guarantee
// on this platform.
func WithLock(_ string, fn func() error) error {
	return fn()
}
