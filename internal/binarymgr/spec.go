// Package binarymgr provides a reusable lifecycle for managed CLI binaries
// distributed through a GitLab project's Generic Package Registry. Each
// consumer describes its binary with a Spec and gets download, checksum
// verification, atomic install, scheduled update checks, and run-time
// pass-through orchestration in return.
package binarymgr

// Spec describes a single managed binary. Everything that varies between
// consumers (project, naming, supported platforms, optional tarball
// extraction) lives here; the Manager and Runner contain no consumer-specific
// strings.
type Spec struct {
	// DisplayName appears in user-facing prompts and log lines
	// (e.g. "GitLab Duo CLI", "Orbit local CLI").
	DisplayName string

	// ProjectID is the GitLab project ID that hosts the generic package.
	ProjectID string

	// PackageName is the generic-package name (e.g. "duo-cli", "orbit-cli").
	PackageName string

	// ConfigPrefix is the config-key namespace. Keys are derived as
	// <prefix>_binary_path, _binary_version, _binary_checksum,
	// _last_update_check, _auto_run, _auto_download.
	ConfigPrefix string

	// EnvVarPrefix is the env-var namespace. Variables are derived as
	// <prefix>_BINARY_PATH, _CHECK_UPDATE.
	EnvVarPrefix string

	// MaxCompatibleMajor caps automatic updates to this major version.
	// 0 disables the cap (every newer version is considered compatible).
	MaxCompatibleMajor int

	// MinVersion forces a re-download when the installed version is below it; empty disables the floor.
	MinVersion string

	// SupportedOS lists the GOOS values the binary is published for.
	SupportedOS []string

	// NormalizeArch converts (GOOS, GOARCH) into the upstream arch name
	// used in asset filenames (e.g. amd64 -> "x64" for duo, "x86_64"
	// for orbit). Should return ErrUnsupportedPlatform-wrapped errors
	// for unsupported combinations.
	NormalizeArch func(goos, goarch string) (string, error)

	// AssetName returns the upstream asset filename for a (os, arch) pair
	// (e.g. "duo-darwin-arm64", "orbit-cli-darwin-aarch64.tar.gz").
	AssetName func(os, arch string) string

	// InstalledName returns the local filename of the installed binary
	// (e.g. "duo", "duo.exe", "orbit"). The install directory is shared
	// across managed binaries (<config-dir>/bin).
	InstalledName func(os string) string

	// Extract optionally transforms the downloaded asset into a binary on
	// disk. nil means the downloaded file IS the binary. TarGzExtractor is
	// the typical choice for tarball-distributed binaries.
	Extract Extractor
}

// Extractor pulls the executable out of a downloaded asset and writes it to
// destDir. It returns the path to the resulting binary file.
type Extractor func(srcPath, destDir string) (binaryPath string, err error)

// configKey returns the fully qualified config key for a suffix.
func (s Spec) configKey(suffix string) string {
	return s.ConfigPrefix + "_" + suffix
}

// envVar returns the fully qualified env var for a suffix.
func (s Spec) envVar(suffix string) string {
	return s.EnvVarPrefix + "_" + suffix
}
