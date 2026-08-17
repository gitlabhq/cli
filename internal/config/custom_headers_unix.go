//go:build unix

package config

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts cmd in its own process group so killProcessGroup can
// terminate it and any children it spawns (for example the `sh -c '...'`
// wrapper the README recommends for pipelines).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
