//go:build !integration

package binaries

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
)

func TestMinderCLI_Wiring(t *testing.T) {
	t.Parallel()

	s := MinderCLI()
	assert.Equal(t, "GitLab Endpoint Agent Platform", s.DisplayName)
	assert.Equal(t, "87908878", s.ProjectID)
	assert.Equal(t, "agent-client", s.PackageName)
	assert.Equal(t, "minder", s.Command)
	assert.Equal(t, "minder_cli", s.ConfigPrefix)
	assert.Equal(t, "GLAB_MINDER_CLI", s.EnvVarPrefix)
	assert.Empty(t, s.MinVersion)
	assert.Equal(t, 1, s.MaxCompatibleMajor, "auto-updates cap at major 1 (0.x and 1.x allowed); a breaking 2.0 must not install unattended")
	assert.True(t, s.RequireChecksum, "a binary that can block tool calls must never run unverified")
	assert.ElementsMatch(t, []string{"linux"}, s.SupportedOS)
	assert.Nil(t, s.Extract, "agent client ships raw binaries and needs no Extractor")
}

func TestMinderNormalizeArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		goos        string
		goarch      string
		want        string
		expectError bool
	}{
		{name: "amd64 linux", goos: "linux", goarch: "amd64", want: "amd64"},
		{name: "arm64 linux", goos: "linux", goarch: "arm64", want: "arm64"},
		{name: "aarch64 alias", goos: "linux", goarch: "aarch64", want: "arm64"},
		{name: "unsupported arch", goos: "linux", goarch: "386", expectError: true},
		{name: "darwin unsupported", goos: "darwin", goarch: "arm64", expectError: true},
		{name: "windows unsupported", goos: "windows", goarch: "amd64", expectError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := minderNormalizeArch(tc.goos, tc.goarch)
			if tc.expectError {
				require.Error(t, err)
				assert.ErrorIs(t, err, binarymgr.ErrUnsupportedPlatform)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestMinderAssetName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "agent-client-linux-amd64", minderAssetName("linux", "amd64"))
	assert.Equal(t, "agent-client-linux-arm64", minderAssetName("linux", "arm64"))
}

func TestMinderInstalledName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "agent-client", minderInstalledName("linux"))
}
