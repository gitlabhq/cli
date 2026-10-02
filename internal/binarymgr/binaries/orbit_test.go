//go:build !integration

package binaries

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
)

func TestOrbit_Wiring(t *testing.T) {
	t.Parallel()

	s := Orbit()
	assert.Equal(t, "GitLab Orbit CLI", s.DisplayName)
	assert.Equal(t, "77960826", s.ProjectID)
	assert.Equal(t, "orbit-cli", s.PackageName)
	assert.Equal(t, "orbit", s.Command)
	assert.Equal(t, "orbit_cli", s.ConfigPrefix)
	assert.Equal(t, "GLAB_ORBIT_CLI", s.EnvVarPrefix)
	assert.Equal(t, "0.130.0", s.MinVersion)
	assert.Zero(t, s.MaxCompatibleMajor, "Orbit is pre-1.0; major-version cap should be uncapped")
	assert.ElementsMatch(t, []string{"darwin", "linux", "windows"}, s.SupportedOS)
	assert.NotNil(t, s.Extract, "Orbit ships archives and requires an Extractor")
}

func TestOrbitNormalizeArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		goos        string
		goarch      string
		want        string
		expectError bool
	}{
		{name: "amd64 darwin", goos: "darwin", goarch: "amd64", want: "x86_64"},
		{name: "amd64 linux", goos: "linux", goarch: "amd64", want: "x86_64"},
		{name: "arm64 darwin", goos: "darwin", goarch: "arm64", want: "aarch64"},
		{name: "arm64 linux", goos: "linux", goarch: "arm64", want: "aarch64"},
		{name: "aarch64 alias", goos: "linux", goarch: "aarch64", want: "aarch64"},
		{name: "amd64 windows", goos: "windows", goarch: "amd64", want: "x86_64"},
		{name: "arm64 windows uses x64 emulation", goos: "windows", goarch: "arm64", want: "x86_64"},
		{name: "unsupported arch", goos: "linux", goarch: "386", expectError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := orbitNormalizeArch(tc.goos, tc.goarch)
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

func TestOrbitAssetName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "orbit-cli-darwin-aarch64.tar.gz", orbitAssetName("darwin", "aarch64"))
	assert.Equal(t, "orbit-cli-darwin-x86_64.tar.gz", orbitAssetName("darwin", "x86_64"))
	assert.Equal(t, "orbit-cli-linux-musl-aarch64.tar.gz", orbitAssetName("linux", "aarch64"))
	assert.Equal(t, "orbit-cli-linux-musl-x86_64.tar.gz", orbitAssetName("linux", "x86_64"))
	assert.Equal(t, "orbit-cli-windows-x86_64.zip", orbitAssetName("windows", "x86_64"))
}

func TestOrbitInstalledName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "orbit", orbitInstalledName("darwin"))
	assert.Equal(t, "orbit", orbitInstalledName("linux"))
	assert.Equal(t, "orbit.exe", orbitInstalledName("windows"))
}

func TestOrbitExtractorFor_picksByOS(t *testing.T) {
	t.Parallel()

	tarPath := filepath.Join(t.TempDir(), "src.tmp")
	require.NoError(t, os.WriteFile(tarPath, buildOrbitTarGz(t), 0o644))

	zipPath := filepath.Join(t.TempDir(), "src.tmp")
	require.NoError(t, os.WriteFile(zipPath, buildOrbitZip(t), 0o644))

	tarDest := t.TempDir()
	got, err := orbitExtractorFor("linux")(tarPath, tarDest)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tarDest, "orbit"), got)

	got, err = orbitExtractorFor("darwin")(tarPath, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "orbit", filepath.Base(got))

	zipDest := t.TempDir()
	got, err = orbitExtractorFor("windows")(zipPath, zipDest)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(zipDest, "orbit.exe"), got)
}

func buildOrbitTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("orbit-binary")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "orbit", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func buildOrbitZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "orbit.exe", Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	require.NoError(t, err)
	_, err = w.Write([]byte("orbit-binary"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}
