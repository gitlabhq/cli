//go:build windows

package minder

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// Windows has no exec(), so shell out and exit with the child's status. The
// agent client is published for Linux only; this path exists so the package
// builds on Windows, where the Spec reports ErrUnsupportedPlatform first.
func executeMinder(ctx context.Context, io *iostreams.IOStreams, binaryPath string, args []string) error {
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Stdin = io.In
	cmd.Stdout = io.StdOut
	cmd.Stderr = io.StdErr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return cmdutils.WrapError(err, "failed to execute GitLab Endpoint Agent Platform")
	}
	return nil
}
