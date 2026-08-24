package binarymgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/go-version"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// Executor runs the resolved binary with the given args. The binarymgr
// package leaves this to consumers because the right strategy is
// platform-specific (syscall.Exec on Unix, subprocess on Windows) and may
// also need to inject distribution-specific environment variables.
type Executor func(ctx context.Context, binaryPath string, args []string) error

// Runner orchestrates the per-invocation lifecycle: ensure the binary is
// installed, prompt for run consent, save metadata, schedule a background
// update check, then hand off to Executor.
type Runner struct {
	IO       *iostreams.IOStreams
	Cfg      config.Config
	Spec     Spec
	Manager  *Manager
	Executor Executor

	// Update / Install / Yes mirror the glab-owned flags after the
	// caller has parsed them out of the cobra args. Args holds the
	// remaining pass-through args destined for the managed binary.
	Update  bool
	Install bool
	Yes     bool
	Args    []string

	// UpdateCommand is the glab subcommand chain shown to users in the
	// "Run 'glab X --update'" hint (e.g. "duo cli", "orbit local").
	// Defaults to Spec.ConfigPrefix if empty.
	UpdateCommand string
}

// updateCheckResult is the outcome of an update check.
type updateCheckResult struct {
	hasUpdate       bool
	currentVersion  string
	latestVersion   string
	newMajorVersion string
}

// Run executes the default flow: ensure installed, prompt for consent (when
// needed), and exec the binary with Args.
func (r *Runner) Run(ctx context.Context) error {
	managedPath, err := ManagedBinaryPath(r.Spec)
	if err != nil {
		return err
	}

	installedPath, _ := r.Cfg.Get("", r.Spec.configKey("binary_path"))

	if installedPath != "" && installedPath != managedPath && r.Update {
		color := r.IO.Color()
		r.IO.LogInfof("%s Updates are not applicable when using a custom binary path (%s).\n", color.DotWarnIcon(), installedPath)
		return nil
	}

	if r.Update {
		return r.HandleUpdate(ctx)
	}

	installedVersion, _ := r.Cfg.Get("", r.Spec.configKey("binary_version"))
	installedVersion = r.versionAfterFloor(installedVersion, installedPath, managedPath)
	autoDownload, _ := r.Cfg.Get("", r.Spec.configKey("auto_download"))
	if r.Yes {
		autoDownload = "true"
	}

	result, err := r.Manager.EnsureInstalled(ctx, installedVersion, installedPath, autoDownload)
	if err != nil {
		return err
	}
	info := result.Info

	if info.Path == managedPath {
		r.saveAutoDownloadPreference(result.AutoDownloadPreference)
		if err := r.SaveBinaryInfo(info); err != nil {
			r.warnf("Failed to save binary metadata: %v", err)
		}
		r.checkForUpdates(ctx)
	} else {
		color := r.IO.Color()
		r.IO.LogInfof("%s Using custom %s binary: %s\n", color.DotWarnIcon(), r.Spec.DisplayName, info.Path)
	}

	if !r.Yes {
		if err := r.checkAutoRun(ctx); err != nil {
			return err
		}
	}

	return r.Executor(ctx, info.Path, r.Args)
}

// HandleInstall installs (or refreshes) the managed binary without running
// it. If the user has configured a custom path, the path is reported and
// nothing is downloaded.
func (r *Runner) HandleInstall(ctx context.Context) error {
	managedPath, err := ManagedBinaryPath(r.Spec)
	if err != nil {
		return err
	}

	installedVersion, _ := r.Cfg.Get("", r.Spec.configKey("binary_version"))
	installedPath, _ := r.Cfg.Get("", r.Spec.configKey("binary_path"))
	installedVersion = r.versionAfterFloor(installedVersion, installedPath, managedPath)
	autoDownload, _ := r.Cfg.Get("", r.Spec.configKey("auto_download"))
	if r.Yes {
		autoDownload = "true"
	}

	result, err := r.Manager.EnsureInstalled(ctx, installedVersion, installedPath, autoDownload)
	if err != nil {
		return err
	}
	info := result.Info

	if info.Path != managedPath {
		color := r.IO.Color()
		r.IO.LogInfof("%s Using custom %s binary: %s\n", color.DotWarnIcon(), r.Spec.DisplayName, info.Path)
		return nil
	}

	if installedVersion != "" && info.Version == installedVersion {
		color := r.IO.Color()
		r.IO.LogInfof("%s %s version %s is already installed.\n", color.GreenCheck(), r.Spec.DisplayName, installedVersion)
		return nil
	}

	r.saveAutoDownloadPreference(result.AutoDownloadPreference)
	r.saveLastUpdateCheck(time.Now())
	return r.SaveBinaryInfo(info)
}

// HandleUpdate forces an update check and applies any newer compatible
// version. If nothing is installed yet, it falls back to a fresh install.
func (r *Runner) HandleUpdate(ctx context.Context) error {
	result, err := r.performUpdateCheck(ctx, true)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if result == nil {
		r.IO.LogInfo("No version installed, downloading latest...\n")
		installedVersion, _ := r.Cfg.Get("", r.Spec.configKey("binary_version"))
		installedPath, _ := r.Cfg.Get("", r.Spec.configKey("binary_path"))
		autoDownload, _ := r.Cfg.Get("", r.Spec.configKey("auto_download"))
		if r.Yes {
			autoDownload = "true"
		}
		result, err := r.Manager.EnsureInstalled(ctx, installedVersion, installedPath, autoDownload)
		if err != nil {
			return err
		}
		r.saveAutoDownloadPreference(result.AutoDownloadPreference)
		r.saveLastUpdateCheck(time.Now())
		return r.SaveBinaryInfo(result.Info)
	}

	if !result.hasUpdate {
		color := r.IO.Color()
		r.IO.LogInfof("%s You are already using the latest compatible version (%s)\n", color.GreenCheck(), result.currentVersion)
		if result.newMajorVersion != "" {
			r.IO.LogInfof("%s %s %s is available but requires a newer version of glab.\n", color.DotWarnIcon(), r.Spec.DisplayName, result.newMajorVersion)
			r.IO.LogInfof("Run 'glab check-update' to check for the latest glab version.\n")
		}
		return nil
	}

	r.IO.LogInfof("Update available: %s → %s\n", result.currentVersion, result.latestVersion)

	info, err := r.Manager.Update(ctx)
	if err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	if err := r.SaveBinaryInfo(info); err != nil {
		r.warnf("Failed to save binary metadata: %v", err)
	}

	color := r.IO.Color()
	r.IO.LogInfof("%s Successfully updated to version %s\n", color.GreenCheck(), result.latestVersion)
	return nil
}

// SaveBinaryInfo writes path/version/checksum to config and persists.
func (r *Runner) SaveBinaryInfo(info *BinaryInfo) error {
	if err := r.Cfg.Set("", r.Spec.configKey("binary_path"), info.Path); err != nil {
		return err
	}
	if err := r.Cfg.Set("", r.Spec.configKey("binary_version"), info.Version); err != nil {
		return err
	}
	if err := r.Cfg.Set("", r.Spec.configKey("binary_checksum"), info.Checksum); err != nil {
		return err
	}
	return r.Cfg.Write()
}

// ShouldForceUpdateCheck returns true when the spec's force-update env var
// is "true". Useful for tests and CI that want to bypass the 24h cache.
func (r *Runner) ShouldForceUpdateCheck() bool {
	return os.Getenv(r.Spec.envVar("CHECK_UPDATE")) == "true"
}

func (r *Runner) versionAfterFloor(installedVersion, installedPath, managedPath string) string {
	if installedPath != "" && installedPath != managedPath {
		return installedVersion
	}
	if r.Spec.MinVersion == "" || installedVersion == "" {
		return installedVersion
	}
	floor, err := version.NewVersion(r.Spec.MinVersion)
	if err != nil {
		return installedVersion
	}
	installed, err := version.NewVersion(installedVersion)
	if err != nil || !installed.LessThan(floor) {
		return installedVersion
	}
	color := r.IO.Color()
	r.IO.LogInfof("%s Installed %s version %s is older than the required %s\n", color.DotWarnIcon(), r.Spec.DisplayName, installedVersion, r.Spec.MinVersion)
	return ""
}

func (r *Runner) performUpdateCheck(ctx context.Context, forceCheck bool) (*updateCheckResult, error) {
	currentVersion, _ := r.Cfg.Get("", r.Spec.configKey("binary_version"))
	if currentVersion == "" {
		return nil, nil
	}

	lastCheckStr, _ := r.Cfg.Get("", r.Spec.configKey("last_update_check"))
	var lastCheckTime time.Time
	if lastCheckStr != "" {
		lastCheckTime, _ = time.Parse(time.RFC3339, lastCheckStr)
	}

	check, err := r.Manager.CheckForUpdate(ctx, currentVersion, lastCheckTime, forceCheck)
	if err != nil {
		return nil, err
	}

	if !check.NewCheckTime.IsZero() {
		if err := r.Cfg.Set("", r.Spec.configKey("last_update_check"), check.NewCheckTime.Format(time.RFC3339)); err != nil {
			r.warnf("Failed to save update check time: %v", err)
		}
		if err := r.Cfg.Write(); err != nil {
			r.warnf("Failed to write config: %v", err)
		}
	}

	return &updateCheckResult{
		hasUpdate:       check.HasUpdate,
		currentVersion:  currentVersion,
		latestVersion:   check.LatestVersion,
		newMajorVersion: check.NewMajorVersion,
	}, nil
}

func (r *Runner) checkAutoRun(ctx context.Context) error {
	autoRun, _ := r.Cfg.Get("", r.Spec.configKey("auto_run"))
	if autoRun == "true" {
		return nil
	}

	if !r.IO.CanPrompt() {
		return nil
	}

	confirm := true
	if err := r.IO.Confirm(ctx, &confirm, fmt.Sprintf("Run the %s?", r.Spec.DisplayName)); err != nil {
		return err
	}
	if !confirm {
		return errors.New("execution cancelled")
	}

	var always bool
	if err := r.IO.Confirm(ctx, &always, "Always run without prompting?"); err != nil {
		r.warnf("Failed to get auto-run preference: %v", err)
		return nil
	}

	if always {
		if err := r.Cfg.Set("", r.Spec.configKey("auto_run"), "true"); err != nil {
			r.warnf("Failed to save preference: %v", err)
		}
		if err := r.Cfg.Write(); err != nil {
			r.warnf("Failed to write config: %v", err)
		}
	}
	return nil
}

func (r *Runner) checkForUpdates(ctx context.Context) {
	result, err := r.performUpdateCheck(ctx, r.ShouldForceUpdateCheck())
	if err != nil || result == nil {
		return
	}

	color := r.IO.Color()
	if result.hasUpdate {
		r.IO.LogInfof("\n%s New %s version available: %s → %s\n", color.DotWarnIcon(), r.Spec.DisplayName, result.currentVersion, result.latestVersion)
		r.IO.LogInfof("Run 'glab %s --update' to update to the latest version\n", r.updateCommand())
	}
	if result.newMajorVersion != "" {
		r.IO.LogInfof("\n%s %s %s is available but requires a newer version of glab.\n", color.DotWarnIcon(), r.Spec.DisplayName, result.newMajorVersion)
		r.IO.LogInfof("Run 'glab check-update' to check for the latest glab version.\n")
	}
}

// updateCommand is the glab subcommand chain users run to update this
// binary. Spec doesn't carry it because the runner is constructed inside
// the command package, so callers can override it via UpdateCommand.
func (r *Runner) updateCommand() string {
	if r.UpdateCommand != "" {
		return r.UpdateCommand
	}
	return r.Spec.ConfigPrefix
}

// saveAutoDownloadPreference persists the user's "always download updates"
// choice. EnsureInstalled returns "true" when the user opted in during the
// follow-up prompt and "" otherwise; we only write on opt-in so this is a
// no-op for the common case.
func (r *Runner) saveAutoDownloadPreference(pref string) {
	if pref == "" {
		return
	}
	if err := r.Cfg.Set("", r.Spec.configKey("auto_download"), pref); err != nil {
		r.warnf("Failed to save preference: %v", err)
		return
	}
	if err := r.Cfg.Write(); err != nil {
		r.warnf("Failed to write config: %v", err)
	}
}

// saveLastUpdateCheck stamps the moment a known-current install was placed
// on disk so the next interactive run skips the redundant registry hit.
func (r *Runner) saveLastUpdateCheck(t time.Time) {
	if err := r.Cfg.Set("", r.Spec.configKey("last_update_check"), t.Format(time.RFC3339)); err != nil {
		r.warnf("Failed to save update check time: %v", err)
		return
	}
	if err := r.Cfg.Write(); err != nil {
		r.warnf("Failed to write config: %v", err)
	}
}

func (r *Runner) warnf(format string, args ...any) {
	color := r.IO.Color()
	r.IO.LogInfof("%s "+format+"\n", append([]any{color.DotWarnIcon()}, args...)...)
}
