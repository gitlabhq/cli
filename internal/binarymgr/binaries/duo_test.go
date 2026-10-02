//go:build !integration

package binaries

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
)

func TestDuoCLI_Wiring(t *testing.T) {
	t.Parallel()

	s := DuoCLI()
	assert.Equal(t, "GitLab Duo CLI", s.DisplayName)
	assert.Equal(t, "46519181", s.ProjectID)
	assert.Equal(t, "duo-cli", s.PackageName)
	assert.Equal(t, "duo cli", s.Command)
	assert.Equal(t, "duo_cli", s.ConfigPrefix)
	assert.Equal(t, "GLAB_DUO_CLI", s.EnvVarPrefix)
	assert.Equal(t, duoMaxCompatibleMajorVersion, s.MaxCompatibleMajor)
	assert.ElementsMatch(t, []string{"darwin", "linux", "windows"}, s.SupportedOS)
	assert.Nil(t, s.Extract, "Duo ships raw binaries; no extractor expected")
}

func TestDuoNormalizeArch(t *testing.T) {
	t.Parallel()

	baseline := func() string { return "x64" }
	modern := func() string { return "x64-modern" }

	tests := []struct {
		name        string
		goos        string
		goarch      string
		linuxX64    func() string
		want        string
		expectError bool
	}{
		{name: "amd64 darwin", goos: "darwin", goarch: "amd64", linuxX64: baseline, want: "x64"},
		{name: "amd64 linux baseline (no AVX2)", goos: "linux", goarch: "amd64", linuxX64: baseline, want: "x64"},
		{name: "amd64 linux modern (AVX2)", goos: "linux", goarch: "amd64", linuxX64: modern, want: "x64-modern"},
		{name: "amd64 windows", goos: "windows", goarch: "amd64", linuxX64: baseline, want: "x64-baseline"},
		{name: "arm64 darwin", goos: "darwin", goarch: "arm64", linuxX64: baseline, want: "arm64"},
		{name: "arm64 linux", goos: "linux", goarch: "arm64", linuxX64: baseline, want: "arm64"},
		{name: "arm64 windows", goos: "windows", goarch: "arm64", linuxX64: baseline, want: "arm64"},
		{name: "aarch64 alias", goos: "linux", goarch: "aarch64", linuxX64: baseline, want: "arm64"},
		{name: "unsupported arch", goos: "linux", goarch: "386", linuxX64: baseline, expectError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := duoNormalizeArchFor(tc.goos, tc.goarch, tc.linuxX64)
			if tc.expectError {
				require.Error(t, err)
				require.ErrorIs(t, err, binarymgr.ErrUnsupportedPlatform)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestDetectLinuxX64ArchVariant(t *testing.T) {
	t.Parallel()
	// Host CPU is unknown at test time; just assert the detector returns
	// one of the two valid variants.
	got := detectLinuxX64ArchVariant()
	assert.Contains(t, []string{"x64", "x64-modern"}, got)
}

func TestDuoAssetName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "duo-darwin-arm64", duoAssetName("darwin", "arm64"))
	assert.Equal(t, "duo-linux-x64", duoAssetName("linux", "x64"))
	assert.Equal(t, "duo-linux-x64-modern", duoAssetName("linux", "x64-modern"))
	assert.Equal(t, "duo-windows-x64-baseline.exe", duoAssetName("windows", "x64-baseline"))
	assert.Equal(t, "duo-windows-arm64.exe", duoAssetName("windows", "arm64"))
}

func TestDuoInstalledName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "duo", duoInstalledName("darwin"))
	assert.Equal(t, "duo", duoInstalledName("linux"))
	assert.Equal(t, "duo.exe", duoInstalledName("windows"))
}
