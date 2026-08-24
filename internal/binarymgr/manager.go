package binarymgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"time"

	"github.com/hashicorp/go-version"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const defaultUpdateCheckDelay = 24 * time.Hour

// BinaryInfo describes an installed (or located) managed binary.
type BinaryInfo struct {
	Path     string
	Version  string
	Checksum string
}

// packageAsset is the resolved upstream asset to download.
type packageAsset struct {
	version  string
	filename string
	checksum string
}

// Manager handles the lifecycle (install, update, validate) of a single
// managed binary described by Spec.
type Manager struct {
	io     *iostreams.IOStreams
	spec   Spec
	client *gitlab.Client
}

// NewManager constructs a Manager for spec. Network requests use an
// unauthenticated client because package downloads are public.
func NewManager(io *iostreams.IOStreams, spec Spec) *Manager {
	client, _ := gitlab.NewAuthSourceClient(gitlab.Unauthenticated{})
	return &Manager{io: io, spec: spec, client: client}
}

// EnsureResult is what EnsureInstalled returns: the resolved binary plus
// metadata the runner needs to react to.
type EnsureResult struct {
	Info *BinaryInfo

	// Downloaded is true when this call actually downloaded and installed
	// the binary (vs. returning an existing installation or a custom path).
	Downloaded bool

	// AutoDownloadPreference is set to "true" when the user opted into
	// automatic future downloads during the prompt. Empty otherwise.
	AutoDownloadPreference string
}

// EnsureInstalled returns a usable binary path, prompting the user to
// download if necessary. installedPath/installedVersion come from saved
// config. If installedPath points outside the managed install directory it
// is treated as a user-provided custom binary and returned without download.
func (m *Manager) EnsureInstalled(ctx context.Context, installedVersion, installedPath, autoDownload string) (*EnsureResult, error) {
	platform, err := detectPlatform(m.spec)
	if err != nil {
		return nil, err
	}

	managedPath := binaryPath(m.spec, platform)

	if installedPath != "" && installedPath != managedPath {
		if err := validateBinaryPath(installedPath, m.spec); err != nil {
			return nil, err
		}
		return &EnsureResult{
			Info: &BinaryInfo{Path: installedPath, Version: installedVersion},
		}, nil
	}

	if installedVersion != "" && m.isBinaryValid(managedPath) {
		return &EnsureResult{
			Info: &BinaryInfo{Path: managedPath, Version: installedVersion},
		}, nil
	}

	shouldDownload, preference, err := m.promptDownload(ctx, autoDownload)
	if err != nil {
		return nil, err
	}
	if !shouldDownload {
		return nil, errors.New("download cancelled")
	}

	info, err := m.downloadAndInstall(ctx, platform)
	if err != nil {
		return nil, err
	}
	return &EnsureResult{Info: info, Downloaded: true, AutoDownloadPreference: preference}, nil
}

// UpdateCheck is the result of CheckForUpdate. NewCheckTime is non-zero
// when the call actually contacted the registry; callers should persist it.
type UpdateCheck struct {
	HasUpdate       bool
	LatestVersion   string
	NewMajorVersion string // set when latest exceeds Spec.MaxCompatibleMajor
	NewCheckTime    time.Time
}

// CheckForUpdate reports whether a newer compatible version exists. If the
// latest upstream version exceeds Spec.MaxCompatibleMajor, HasUpdate is
// false and NewMajorVersion is set so callers can surface the gating to
// the user.
func (m *Manager) CheckForUpdate(ctx context.Context, currentVersion string, lastCheckTime time.Time, forceCheck bool) (UpdateCheck, error) {
	if !forceCheck && !lastCheckTime.IsZero() && time.Since(lastCheckTime) < defaultUpdateCheckDelay {
		return UpdateCheck{}, nil
	}

	latestPkg, err := m.fetchLatestPackage(ctx)
	if err != nil {
		return UpdateCheck{}, err
	}

	now := time.Now()

	latestV, err := version.NewVersion(latestPkg.Version)
	if err != nil {
		return UpdateCheck{NewCheckTime: now}, fmt.Errorf("invalid latest version: %w", err)
	}

	if m.spec.MaxCompatibleMajor > 0 {
		segs := latestV.Segments()
		if len(segs) == 0 {
			return UpdateCheck{NewCheckTime: now}, fmt.Errorf("invalid latest version: %q", latestPkg.Version)
		}
		if segs[0] > m.spec.MaxCompatibleMajor {
			return UpdateCheck{NewMajorVersion: latestPkg.Version, NewCheckTime: now}, nil
		}
	}

	current, err := version.NewVersion(currentVersion)
	if err != nil {
		return UpdateCheck{NewCheckTime: now}, fmt.Errorf("invalid current version: %w", err)
	}

	result := UpdateCheck{LatestVersion: latestPkg.Version, NewCheckTime: now}
	if latestV.GreaterThan(current) {
		result.HasUpdate = true
	}
	return result, nil
}

// Update downloads and installs the latest version unconditionally.
func (m *Manager) Update(ctx context.Context) (*BinaryInfo, error) {
	platform, err := detectPlatform(m.spec)
	if err != nil {
		return nil, err
	}
	return m.downloadAndInstall(ctx, platform)
}

// validateBinaryPath verifies that a custom binary path is usable.
//
// Error messages avoid leading with the env-var name because fang
// Title-cases the first token of an error ("Glab_orbit_local_..."), and
// they name both configuration sources (env var + config key) since either
// can set the value.
func validateBinaryPath(path string, spec Spec) error {
	source := fmt.Sprintf("the %s env var or the %s config key", spec.envVar("BINARY_PATH"), spec.configKey("binary_path"))
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("custom %s binary path %q (set via %s) was not found. Check that the path is correct", spec.DisplayName, path, source)
		}
		return fmt.Errorf("custom %s binary path %q (set via %s) could not be accessed: %w", spec.DisplayName, path, source, err)
	}
	if info.IsDir() {
		return fmt.Errorf("custom %s binary path %q (set via %s) is a directory, not an executable file", spec.DisplayName, path, source)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("custom %s binary path %q (set via %s) is not executable. Run: chmod +x %s", spec.DisplayName, path, source, path)
	}
	return nil
}

func (m *Manager) isBinaryValid(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return false
	}
	return true
}

// promptDownload asks the user to confirm a download. If they accept, it
// follows up with an "always?" prompt. Returns (shouldDownload, preference,
// err) where preference is "true" iff the user wants future downloads to
// proceed automatically.
func (m *Manager) promptDownload(ctx context.Context, autoDownload string) (bool, string, error) {
	if autoDownload == "true" {
		return true, "", nil
	}

	if !m.io.CanPrompt() {
		return false, "", consentRequiredError(m.spec, "auto_download", fmt.Sprintf("download the %s binary", m.spec.DisplayName))
	}

	confirm := true
	if err := m.io.Confirm(ctx, &confirm, fmt.Sprintf("Download %s binary?", m.spec.DisplayName)); err != nil {
		return false, "", err
	}
	if !confirm {
		return false, "", errors.New("download cancelled")
	}

	var always bool
	if err := m.io.Confirm(ctx, &always, "Always download updates automatically?"); err != nil {
		return true, "", nil //nolint:nilerr // proceed with download even if the secondary "always" prompt fails (e.g. non-interactive)
	}
	if always {
		return true, "true", nil
	}
	return true, "", nil
}

func (m *Manager) downloadAndInstall(ctx context.Context, platform Platform) (*BinaryInfo, error) {
	asset, err := m.fetchPackageAsset(ctx, platform)
	if err != nil {
		return nil, err
	}

	tempFile, err := os.CreateTemp("", "binarymgr-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	m.io.LogInfof("Downloading %s version %s...\n", asset.filename, asset.version)
	if err := m.downloadFile(ctx, asset, tempFile); err != nil {
		return nil, err
	}

	if err := m.verifyChecksum(tempFile.Name(), asset.checksum); err != nil {
		return nil, err
	}

	binarySrc := tempFile.Name()
	if m.spec.Extract != nil {
		extractDir, err := os.MkdirTemp("", "binarymgr-extract-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create extract directory: %w", err)
		}
		defer os.RemoveAll(extractDir)

		extracted, err := m.spec.Extract(tempFile.Name(), extractDir)
		if err != nil {
			return nil, fmt.Errorf("failed to extract binary: %w", err)
		}
		binarySrc = extracted
	}

	if err := m.installBinary(binarySrc, platform); err != nil {
		return nil, err
	}

	finalPath := binaryPath(m.spec, platform)
	color := m.io.Color()
	m.io.LogInfof("%s Installed to: %s\n", color.GreenCheck(), finalPath)

	return &BinaryInfo{
		Path:     finalPath,
		Version:  asset.version,
		Checksum: asset.checksum,
	}, nil
}

// fetchLatestPackage returns the package with the highest semantic version
// in the registry. The GitLab packages API sorts `order_by=version`
// lexicographically (8.99.0 > 8.100.0), so we collect all packages and pick
// the max via hashicorp/go-version client-side. Non-parseable versions are
// skipped — the generic packages API permits any version string.
func (m *Manager) fetchLatestPackage(ctx context.Context) (*gitlab.Package, error) {
	opts := &gitlab.ListProjectPackagesOptions{
		PackageType: new("generic"),
		PackageName: &m.spec.PackageName,
		OrderBy:     new("created_at"),
		Sort:        new("desc"),
	}
	packages, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Package, *gitlab.Response, error) {
		return m.client.Packages.ListProjectPackages(m.spec.ProjectID, opts, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch packages: %w", err)
	}
	if len(packages) == 0 {
		return nil, errors.New("no packages found")
	}

	var latestPkg *gitlab.Package
	var latestVer *version.Version
	for _, pkg := range packages {
		v, err := version.NewVersion(pkg.Version)
		if err != nil {
			continue
		}
		if latestVer == nil || v.GreaterThan(latestVer) {
			latestPkg, latestVer = pkg, v
		}
	}
	if latestPkg == nil {
		return nil, errors.New("no packages with parseable versions found")
	}
	return latestPkg, nil
}

func (m *Manager) fetchPackageAsset(ctx context.Context, platform Platform) (*packageAsset, error) {
	pkg, err := m.fetchLatestPackage(ctx)
	if err != nil {
		return nil, err
	}

	if m.spec.MaxCompatibleMajor > 0 {
		latestV, err := version.NewVersion(pkg.Version)
		if err != nil {
			return nil, fmt.Errorf("invalid package version %q: %w", pkg.Version, err)
		}
		segs := latestV.Segments()
		if len(segs) == 0 || segs[0] > m.spec.MaxCompatibleMajor {
			return nil, fmt.Errorf("no compatible %s version found (major version %d supported; %s requires a newer glab)", m.spec.DisplayName, m.spec.MaxCompatibleMajor, pkg.Version)
		}
	}

	files, _, err := m.client.Packages.ListPackageFiles(m.spec.ProjectID, pkg.ID, &gitlab.ListPackageFilesOptions{}, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package files: %w", err)
	}

	wanted := assetName(m.spec, platform)
	idx := slices.IndexFunc(files, func(file *gitlab.PackageFile) bool {
		return file.FileName == wanted
	})
	if idx == -1 {
		return nil, fmt.Errorf("binary not found for platform: %s", wanted)
	}

	file := files[idx]
	return &packageAsset{
		version:  pkg.Version,
		filename: file.FileName,
		checksum: file.FileSHA256,
	}, nil
}

func (m *Manager) downloadFile(ctx context.Context, asset *packageAsset, dest *os.File) error {
	data, _, err := m.client.GenericPackages.DownloadPackageFile(
		m.spec.ProjectID,
		m.spec.PackageName,
		asset.version,
		asset.filename,
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	if _, err := dest.Write(data); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func (m *Manager) verifyChecksum(filePath, expectedChecksum string) error {
	if expectedChecksum == "" {
		color := m.io.Color()
		m.io.LogInfof("%s No checksum available, skipping verification\n", color.DotWarnIcon())
		return nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for checksum: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedChecksum {
		return fmt.Errorf("checksum verification failed: expected %s, got %s", expectedChecksum, actual)
	}

	color := m.io.Color()
	m.io.LogInfof("%s Checksum verified\n", color.GreenCheck())
	return nil
}

// installBinary atomically places the binary at its final managed path.
func (m *Manager) installBinary(srcPath string, platform Platform) error {
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	dest := binaryPath(m.spec, platform)

	tmp, err := os.CreateTemp(dir, ".binarymgr-install-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file in install directory: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	src, err := os.Open(srcPath)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("failed to open source binary: %w", err)
	}
	defer src.Close()

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to copy binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}
	return nil
}
