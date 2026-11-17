//go:build !windows

package sync

import "syscall"

// detachedProcAttr starts the process in a new session, so it outlives the
// agent's process group.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
