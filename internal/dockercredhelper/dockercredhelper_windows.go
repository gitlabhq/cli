//go:build windows

package dockercredhelper

import (
	"fmt"
	"runtime"
)

// Supported always fails: the shell-wrapper shim only works on
// POSIX-compliant operating systems. On Windows, Docker resolves
// docker-credential-glab.exe or .bat, neither of which script is.
//
// Callers that do other work first, such as a token exchange, should check
// this before that work rather than waiting for Install to fail.
//
// See https://gitlab.com/gitlab-org/cli/-/issues/7906 for the work on
// Windows support.
func Supported() error {
	return fmt.Errorf("operating system %q is not supported; "+
		"only Linux and MacOS (Darwin) are supported", runtime.GOOS)
}

// Install always fails, for the reason Supported gives.
func Install() (string, error) {
	return "", Supported()
}
