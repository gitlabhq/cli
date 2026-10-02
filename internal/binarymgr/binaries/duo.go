package binaries

import (
	"golang.org/x/sys/cpu"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
)

// duoMaxCompatibleMajorVersion caps Duo CLI auto-updates to this major
// version. Bump after validating compatibility against the new major.
const duoMaxCompatibleMajorVersion = 9

// DuoCLI describes the GitLab Duo CLI binary run by `glab duo cli`.
func DuoCLI() binarymgr.Spec {
	return binarymgr.Spec{
		DisplayName:        "GitLab Duo CLI",
		ProjectID:          "46519181",
		PackageName:        "duo-cli",
		Command:            "duo cli",
		ConfigPrefix:       "duo_cli",
		EnvVarPrefix:       "GLAB_DUO_CLI",
		MaxCompatibleMajor: duoMaxCompatibleMajorVersion,
		SupportedOS:        []string{"darwin", "linux", "windows"},
		NormalizeArch:      duoNormalizeArch,
		AssetName:          duoAssetName,
		InstalledName:      duoInstalledName,
		// Duo ships raw binaries — no extraction needed.
		Extract: nil,
	}
}

func duoNormalizeArch(goos, goarch string) (string, error) {
	return duoNormalizeArchFor(goos, goarch, detectLinuxX64ArchVariant)
}

// duoNormalizeArchFor is duoNormalizeArch's testable core. The Linux x64
// variant detector is injected so tests don't depend on the host CPU.
func duoNormalizeArchFor(goos, goarch string, linuxX64Variant func() string) (string, error) {
	switch goarch {
	case "amd64":
		switch goos {
		case "windows":
			return "x64-baseline", nil
		case "linux":
			return linuxX64Variant(), nil
		}
		return "x64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	}
	return "", binarymgr.ErrUnsupportedPlatform
}

// detectLinuxX64ArchVariant mirrors the upstream Duo CLI installer's
// detect_linux_x64_variant: "x64-modern" when the host CPU advertises AVX2,
// otherwise "x64" (baseline).
//
// AVX2 is a sufficient proxy for the full feature set Bun's modern target
// requires (AVX2 + BMI2 + FMA): every CPU that advertises AVX2 also has
// BMI2 and FMA (Haswell+ / Excavator+), so checking AVX2 alone matches the
// upstream installer's bash detect_linux_x64_variant exactly.
func detectLinuxX64ArchVariant() string {
	// Linux uses "x64" instead of "x64-baseline" to preserve
	// compatibility with previous versions of glab as x64 should work on most systems
	// and it matches the duo cli install script:
	// https://gitlab.com/gitlab-org/editor-extensions/gitlab-lsp/-/blob/247ec22ab64e4160ff5776c4254598623f537738/packages/cli/scripts/install_duo_cli.sh
	if cpu.X86.HasAVX2 {
		return "x64-modern"
	}
	return "x64"
}

func duoAssetName(goos, arch string) string {
	name := "duo-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func duoInstalledName(goos string) string {
	if goos == "windows" {
		return "duo.exe"
	}
	return "duo"
}
