//go:build !linux && !darwin && !windows

package dockercredhelper

import (
	"fmt"
	"runtime"
)

// Install always fails: the shell-wrapper shim only works on
// POSIX-compliant operating systems. This build tag covers everything glab
// targets other than Linux, macOS, and Windows, including freebsd, which
// .goreleaser.yml also builds for.
//
// See https://gitlab.com/gitlab-org/cli/-/issues/7906 for the work on
// support for additional operating systems.
func Install() (string, error) {
	return "", fmt.Errorf("operating system %q is not supported; "+
		"only Linux and MacOS (Darwin) are supported", runtime.GOOS)
}
