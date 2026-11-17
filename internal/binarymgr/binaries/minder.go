package binaries

import (
	"gitlab.com/gitlab-org/cli/internal/binarymgr"
)

// MinderCLI describes the GitLab Endpoint Agent Platform binary run by `glab minder`.
// MaxCompatibleMajor caps auto-updates at major version 1, so the current 0.x
// line and the first stable 1.x line install unattended, but a breaking 2.0
// never does.
func MinderCLI() binarymgr.Spec {
	return binarymgr.Spec{
		DisplayName:  "GitLab Endpoint Agent Platform",
		ProjectID:    "87908878",
		PackageName:  "agent-client",
		Command:      "minder",
		ConfigPrefix: "minder_cli",
		EnvVarPrefix: "GLAB_MINDER_CLI",
		// The client can block tool calls, so an unverified download must never run.
		RequireChecksum:    true,
		MaxCompatibleMajor: 1,
		SupportedOS:        []string{"linux"},
		NormalizeArch:      minderNormalizeArch,
		AssetName:          minderAssetName,
		InstalledName:      minderInstalledName,
		// The client ships raw static ELF binaries — no extraction needed.
		Extract: nil,
	}
}

func minderNormalizeArch(goos, goarch string) (string, error) {
	if goos != "linux" {
		return "", binarymgr.ErrUnsupportedPlatform
	}
	switch goarch {
	case "amd64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	}
	return "", binarymgr.ErrUnsupportedPlatform
}

func minderAssetName(goos, arch string) string {
	return "agent-client-linux-" + arch
}

func minderInstalledName(goos string) string {
	return "agent-client"
}
