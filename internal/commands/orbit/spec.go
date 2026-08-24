package orbit

import (
	"runtime"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// MaxCompatibleMajor is left unset (uncapped) while Orbit is pre-1.0.
func Spec() binarymgr.Spec {
	return binarymgr.Spec{
		DisplayName:   "Orbit CLI",
		ProjectID:     "77960826",
		PackageName:   "orbit-cli",
		ConfigPrefix:  "orbit_local",
		EnvVarPrefix:  "GLAB_ORBIT_LOCAL",
		MinVersion:    "0.103.0",
		SupportedOS:   []string{"darwin", "linux", "windows"},
		NormalizeArch: orbitNormalizeArch,
		AssetName:     orbitAssetName,
		InstalledName: orbitInstalledName,
		Extract:       orbitExtractorFor(runtime.GOOS),
	}
}

func newRunner(io *iostreams.IOStreams, cfg config.Config, spec binarymgr.Spec) *binarymgr.Runner {
	return &binarymgr.Runner{
		IO:            io,
		Cfg:           cfg,
		Spec:          spec,
		Manager:       binarymgr.NewManager(io, spec),
		UpdateCommand: "orbit",
	}
}

// Windows ships only x86_64; ARM64 runs it under x64 emulation.
func orbitNormalizeArch(goos, goarch string) (string, error) {
	if goos == "windows" {
		return "x86_64", nil
	}
	switch goarch {
	case "amd64":
		return "x86_64", nil
	case "arm64", "aarch64":
		return "aarch64", nil
	}
	return "", binarymgr.ErrUnsupportedPlatform
}

// Linux uses the static musl build; the glibc build needs GLIBC>=2.34, absent on minimal hosts.
func orbitAssetName(goos, arch string) string {
	switch goos {
	case "windows":
		return "orbit-cli-" + goos + "-" + arch + ".zip"
	case "linux":
		return "orbit-cli-linux-musl-" + arch + ".tar.gz"
	default: // darwin
		return "orbit-cli-" + goos + "-" + arch + ".tar.gz"
	}
}

func orbitInstalledName(goos string) string {
	if goos == "windows" {
		return "orbit.exe"
	}
	return "orbit"
}

// Keyed on goos because binarymgr downloads to a generic temp suffix, not the asset name.
func orbitExtractorFor(goos string) binarymgr.Extractor {
	if goos == "windows" {
		return binarymgr.ZipExtractor("orbit.exe")
	}
	return binarymgr.TarGzExtractor("orbit")
}
