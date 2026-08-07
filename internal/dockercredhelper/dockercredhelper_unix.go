//go:build linux || darwin

package dockercredhelper

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"gitlab.com/gitlab-org/cli/internal/fsx"
)

// Install writes the shim next to the glab binary found on PATH and returns
// its path. It is idempotent: re-running it overwrites the script and forces
// the mode, so a shim left by an older glab is brought up to date.
func Install() (string, error) {
	glabPath, err := exec.LookPath("glab")
	if err != nil {
		return "", fmt.Errorf("looking up parent directory of glab binary: %w", err)
	}

	path := filepath.Join(filepath.Dir(glabPath), FullName)

	// Docker invokes this one path for every registry it delegates to glab, so
	// the shim is written through fsx.WriteExecutable rather than in place: a
	// process killed mid-write, or a docker-credential-glab invocation racing
	// a concurrent install, must never observe a torn or non-executable
	// script. WriteExecutable lands the file at its final mode as part of the
	// same atomic rename, so unlike a write followed by a separate Chmod,
	// there is no window where the shim exists at its final path but isn't
	// yet executable.
	if err := fsx.WriteExecutable(path, script); err != nil {
		return "", fmt.Errorf("writing %s shim: %w", FullName, err)
	}

	return path, nil
}
