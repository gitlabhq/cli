//go:build unix

package orbit

import (
	"context"
	"os"
	"syscall"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// syscall.Exec hands the terminal and signals straight to the binary.
func executeOrbit(ctx context.Context, io *iostreams.IOStreams, binaryPath string, args []string, extraEnv []string) error {
	_, _ = ctx, io
	argv := append([]string{binaryPath}, args...)
	injected := append([]string{"GITLAB_ORBIT_DISTRIBUTION=glab"}, extraEnv...)
	return syscall.Exec(binaryPath, argv, environWith(os.Environ(), injected))
}
