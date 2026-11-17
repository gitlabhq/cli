//go:build unix

package minder

import (
	"context"
	"os"
	"syscall"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// syscall.Exec hands the terminal and signals straight to the binary.
func executeMinder(ctx context.Context, io *iostreams.IOStreams, binaryPath string, args []string) error {
	_, _ = ctx, io
	argv := append([]string{binaryPath}, args...)
	return syscall.Exec(binaryPath, argv, os.Environ())
}
