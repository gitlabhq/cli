//go:build !windows

package start

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own session so it outlives the short-lived
// `job-proxy start` and runs for the whole job.
func detach(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}
