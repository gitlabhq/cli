//go:build windows

package orbit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// Windows ERROR_BAD_EXE_FORMAT, matched by code since the message is localized.
const errorBadExeFormat = syscall.Errno(193)

// Windows has no exec(), so shell out and exit with the child's status.
func executeOrbit(ctx context.Context, io *iostreams.IOStreams, binaryPath string, args []string, extraEnv []string) error {
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Stdin = io.In
	cmd.Stdout = io.StdOut
	cmd.Stderr = io.StdErr
	injected := append([]string{"GITLAB_ORBIT_DISTRIBUTION=glab"}, extraEnv...)
	cmd.Env = environWith(os.Environ(), injected)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Exit with the child's status to mirror the Unix exec path; don't return the error instead.
			os.Exit(exitErr.ExitCode())
		}
		return wrapExecError(err)
	}
	return nil
}

func wrapExecError(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == errorBadExeFormat {
		if runtime.GOARCH == "arm64" {
			return cmdutils.WrapError(err, fmt.Sprintf(
				"failed to execute Orbit CLI: the x86_64 binary could not run on ARM64 Windows. "+
					"This usually means x64 emulation is not enabled. Upgrade to Windows 11 or enable x64 emulation, "+
					"then retry %q",
				"glab orbit"))
		}
		return cmdutils.WrapError(err, "failed to execute Orbit CLI: the binary appears to be corrupted. Run `glab orbit --update` to reinstall")
	}
	return cmdutils.WrapError(err, "failed to execute Orbit CLI")
}
