package sync

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detachedProcAttr starts the process without a console, so it outlives the
// agent's console.
//
// Best effort, and not tested on Windows: glab minder supports only macOS and
// Linux, and setup installs no hooks on Windows. If the agent runs its hooks in
// a job object that kills its processes when the job closes, this process is
// killed with the hook and the session is not synced.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
}
