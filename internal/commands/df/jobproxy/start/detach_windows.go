//go:build windows

package start

import (
	"errors"
	"os/exec"
)

// detach is unsupported on Windows: the job-level proxy relies on a Unix
// session and the Linux system trust store, so it targets Linux CI/CD jobs.
func detach(*exec.Cmd) error {
	return errors.New("the job-level Dependency Firewall proxy is not supported on Windows")
}
