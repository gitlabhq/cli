//go:build !integration

package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/binarymgr/binaries"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func skipOnUnsupportedPlatform(t *testing.T) {
	t.Helper()
	if _, err := binarymgr.ManagedBinaryPath(binaries.DuoCLI()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
		t.Skipf("skipping on unsupported platform: %v", err)
	}
}

func customBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "duo")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	return path
}

func TestUpdate_CustomBinaryIsNotUpdated(t *testing.T) {
	skipOnUnsupportedPlatform(t)
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	t.Setenv("GLAB_DUO_CLI_BINARY_PATH", customBinary(t))

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("")

	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Updates are not applicable when using a custom binary path")
}

func TestUpdate_YesIsAccepted(t *testing.T) {
	skipOnUnsupportedPlatform(t)
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	t.Setenv("GLAB_DUO_CLI_BINARY_PATH", customBinary(t))

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("--yes")

	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Updates are not applicable when using a custom binary path")
}

func TestUpdate_NotInstalledWithoutYesNeedsConsent(t *testing.T) {
	skipOnUnsupportedPlatform(t)
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("")

	require.ErrorContains(t, err, "download the GitLab Duo CLI binary")
	assert.ErrorContains(t, err, "--yes")
}

func TestUpdate_RejectsArguments(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("now")

	require.ErrorContains(t, err, `unknown command "now"`)
}
