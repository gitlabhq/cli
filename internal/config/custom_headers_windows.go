//go:build windows

package config

import (
	"os/exec"
	"strconv"
)

// setProcessGroup is a no-op on Windows; killProcessGroup terminates the
// process tree directly via taskkill instead of relying on a POSIX-style
// process group.
func setProcessGroup(_ *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
