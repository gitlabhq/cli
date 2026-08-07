//go:build windows

package dockercredhelper

import (
	"fmt"
	"runtime"
)

// Install always fails: the shell-wrapper shim only works on
// POSIX-compliant operating systems. On Windows, Docker resolves
// docker-credential-glab.exe or .bat, neither of which script is.
//
// See https://gitlab.com/gitlab-org/cli/-/issues/7906 for the work on
// Windows support.
func Install() (string, error) {
	return "", fmt.Errorf("operating system %q is not supported; "+
		"only Linux and MacOS (Darwin) are supported", runtime.GOOS)
}
