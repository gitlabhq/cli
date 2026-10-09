// Package fallbacksync manages the scheduled job that runs
// `glab govern audit sync --all` when agent hooks fail to sync a session.
package fallbacksync

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
)

const (
	launchdLabel       = "com.gitlab.glab-govern-audit-sync"
	systemdServiceName = "glab-govern-audit-sync.service"
	systemdTimerName   = "glab-govern-audit-sync.timer"

	// Interval is how often the scheduled job runs.
	Interval = 30 * time.Minute
)

// ErrUnsupportedOS is returned on platforms without launchd or systemd.
var ErrUnsupportedOS = errors.New("fallback periodic sync is supported only on macOS and Linux")

// Unit is a file that makes up the scheduled job.
type Unit struct {
	Path    string
	Content string
}

// Job is what the scheduled job runs.
type Job struct {
	GlabPath string
	// ConfigDir is passed to the job as GLAB_CONFIG_DIR. launchd and systemd
	// start jobs without the user's shell environment, so without it a
	// custom GLAB_CONFIG_DIR or XDG_CONFIG_HOME would point the job at a
	// different directory from the hooks.
	ConfigDir string
}

// Units returns the files that define the scheduled job on goos.
func Units(goos, home string, job Job) ([]Unit, error) {
	switch goos {
	case "darwin":
		return []Unit{{Path: launchdPlistPath(home), Content: launchdPlist(job)}}, nil
	case "linux":
		dir := systemdDir(home)
		return []Unit{
			{Path: filepath.Join(dir, systemdServiceName), Content: systemdService(job)},
			{Path: filepath.Join(dir, systemdTimerName), Content: systemdTimer()},
		}, nil
	default:
		return nil, ErrUnsupportedOS
	}
}

// Description names the scheduled job for prompts and help text.
func Description(goos string) string {
	switch goos {
	case "darwin":
		return fmt.Sprintf("a launchd agent (%s)", launchdPlistPath("~"))
	case "linux":
		return fmt.Sprintf("a systemd user timer (%s)", filepath.Join(systemdDir("~"), systemdTimerName))
	default:
		return "a scheduled job"
	}
}

func launchdPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func systemdDir(home string) string {
	return filepath.Join(home, ".config", "systemd", "user")
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func launchdPlist(job Job) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>govern</string>
        <string>audit</string>
        <string>sync</string>
        <string>--all</string>
        <string>--silent</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>GLAB_CONFIG_DIR</key>
        <string>%s</string>
    </dict>
    <key>StartInterval</key>
    <integer>%d</integer>
    <key>RunAtLoad</key>
    <false/>
</dict>
</plist>
`, launchdLabel, xmlEscape(job.GlabPath), xmlEscape(job.ConfigDir), int(Interval.Seconds()))
}

// systemdQuoter escapes a value for a double-quoted systemd unit setting.
// systemd expands % specifiers even within quotes.
var systemdQuoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%")

var systemdUnquoter = strings.NewReplacer(`\\`, `\`, `\"`, `"`, "%%", "%")

func systemdService(job Job) string {
	return fmt.Sprintf(`[Unit]
Description=GitLab AI agent governance fallback sync

[Service]
Type=oneshot
Environment="GLAB_CONFIG_DIR=%s"
ExecStart="%s" govern audit sync --all --silent
`, systemdQuoter.Replace(job.ConfigDir), systemdQuoter.Replace(job.GlabPath))
}

func systemdTimer() string {
	return fmt.Sprintf(`[Unit]
Description=GitLab AI agent governance fallback sync timer

[Timer]
OnBootSec=5min
OnUnitActiveSec=%dmin

[Install]
WantedBy=timers.target
`, int(Interval.Minutes()))
}

// Install writes the job's files and loads the job, replacing any existing one.
func Install(ctx context.Context, executor cmdutils.Executor, goos, home string, job Job, uid int) error {
	units, err := Units(goos, home, job)
	if err != nil {
		return err
	}
	for _, u := range units {
		if err := os.MkdirAll(filepath.Dir(u.Path), 0o755); err != nil {
			return fmt.Errorf("could not create %s: %w", filepath.Dir(u.Path), err)
		}
		if err := os.WriteFile(u.Path, []byte(u.Content), 0o644); err != nil {
			return fmt.Errorf("could not write %s: %w", u.Path, err)
		}
	}

	if err := load(ctx, executor, goos, units, uid); err != nil {
		// Leaving the files would make doctor report a half-installed job,
		// for example where there is no user launchd domain or systemd bus.
		for _, u := range units {
			if rmErr := os.Remove(u.Path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				err = errors.Join(err, rmErr)
			}
		}
		return err
	}
	return nil
}

func load(ctx context.Context, executor cmdutils.Executor, goos string, units []Unit, uid int) error {
	switch goos {
	case "darwin":
		// bootstrap fails if the label is already loaded, and bootout fails if it is not.
		_, _ = executor.ExecWithCombinedOutput(ctx, "launchctl", []string{"bootout", launchdTarget(uid)}, nil)
		return run(ctx, executor, "launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), units[0].Path)
	default:
		if err := run(ctx, executor, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return run(ctx, executor, "systemctl", "--user", "enable", "--now", systemdTimerName)
	}
}

// Uninstall unloads the job and removes its files. It reports whether any
// files were removed.
func Uninstall(ctx context.Context, executor cmdutils.Executor, goos, home string, uid int) (bool, error) {
	units, err := Units(goos, home, Job{})
	if err != nil {
		return false, err
	}

	// Unloading fails when the job is not loaded, which is the state we want.
	switch goos {
	case "darwin":
		_, _ = executor.ExecWithCombinedOutput(ctx, "launchctl", []string{"bootout", launchdTarget(uid)}, nil)
	default:
		_, _ = executor.ExecWithCombinedOutput(ctx, "systemctl", []string{"--user", "disable", "--now", systemdTimerName}, nil)
	}

	removed := false
	for _, u := range units {
		err := os.Remove(u.Path)
		switch {
		case err == nil:
			removed = true
		case errors.Is(err, fs.ErrNotExist):
		default:
			return removed, fmt.Errorf("could not remove %s: %w", u.Path, err)
		}
	}

	if removed && goos == "linux" {
		if err := run(ctx, executor, "systemctl", "--user", "daemon-reload"); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func launchdTarget(uid int) string {
	return fmt.Sprintf("gui/%d/%s", uid, launchdLabel)
}

func run(ctx context.Context, executor cmdutils.Executor, name string, args ...string) error {
	if out, err := executor.ExecWithCombinedOutput(ctx, name, args, nil); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

// ErrNotInstalled is returned by InstalledBinary when the job's files are absent.
var ErrNotInstalled = errors.New("fallback periodic sync is not installed")

// InstalledBinary returns the glab path that the installed job runs.
func InstalledBinary(goos, home string) (string, error) {
	units, err := Units(goos, home, Job{})
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(units[0].Path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNotInstalled
	}
	if err != nil {
		return "", err
	}

	if goos == "darwin" {
		return plistProgram(data)
	}
	for line := range strings.Lines(string(data)) {
		rest, ok := strings.CutPrefix(line, `ExecStart="`)
		if !ok {
			continue
		}
		if path, ok := cutQuoted(rest); ok {
			return systemdUnquoter.Replace(path), nil
		}
		break
	}
	return "", fmt.Errorf("could not find ExecStart in %s", units[0].Path)
}

// cutQuoted returns s up to its first unescaped double quote.
func cutQuoted(s string) (string, bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return s[:i], true
		}
	}
	return "", false
}

func plistProgram(data []byte) (string, error) {
	var doc struct {
		Dict struct {
			Keys   []string `xml:"key"`
			Arrays []struct {
				Strings []string `xml:"string"`
			} `xml:"array"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("could not parse plist: %w", err)
	}
	if len(doc.Dict.Arrays) == 0 || len(doc.Dict.Arrays[0].Strings) == 0 {
		return "", errors.New("plist has no ProgramArguments")
	}
	return doc.Dict.Arrays[0].Strings[0], nil
}

// JobState is what the system reports about the installed job.
type JobState struct {
	Loaded bool
	// Ran reports whether the job has finished at least once since it was loaded.
	Ran      bool
	ExitCode int
}

// State asks launchd or systemd whether the job is loaded and how its last
// run exited. It catches runs that fail before they can write a Status, such
// as a glab binary that does not support --all.
func State(ctx context.Context, executor cmdutils.Executor, goos string, uid int) (JobState, error) {
	switch goos {
	case "darwin":
		out, err := executor.ExecWithCombinedOutput(ctx, "launchctl", []string{"print", launchdTarget(uid)}, nil)
		if exited(err) {
			// launchctl print exits non-zero when the label is not loaded.
			return JobState{}, nil
		}
		if err != nil {
			return JobState{}, fmt.Errorf("launchctl print: %w", err)
		}
		return launchdState(string(out)), nil
	case "linux":
		_, err := executor.ExecWithCombinedOutput(ctx, "systemctl", []string{"--user", "is-active", "--quiet", systemdTimerName}, nil)
		if exited(err) {
			return JobState{}, nil
		}
		if err != nil {
			return JobState{}, fmt.Errorf("systemctl --user is-active: %w", err)
		}
		out, err := executor.ExecWithCombinedOutput(ctx, "systemctl", []string{"--user", "show", systemdServiceName, "--property=ExecMainStartTimestampMonotonic,ExecMainStatus"}, nil)
		if err != nil {
			return JobState{}, fmt.Errorf("systemctl --user show %s: %w\n%s", systemdServiceName, err, out)
		}
		return systemdState(string(out)), nil
	default:
		return JobState{}, ErrUnsupportedOS
	}
}

// exited reports whether err is a command exiting with a non-zero status, as
// opposed to the command failing to start.
func exited(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func launchdState(out string) JobState {
	state := JobState{Loaded: true}
	for line := range strings.Lines(out) {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "last exit code = ")
		if !ok {
			continue
		}
		// The value is "(never exited)" before the first run.
		first, _, _ := strings.Cut(value, " ")
		if code, err := strconv.Atoi(first); err == nil {
			state.Ran = true
			state.ExitCode = code
		}
		break
	}
	return state
}

func systemdState(out string) JobState {
	state := JobState{Loaded: true}
	for line := range strings.Lines(out) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "ExecMainStartTimestampMonotonic":
			state.Ran = value != "" && value != "0"
		case "ExecMainStatus":
			state.ExitCode, _ = strconv.Atoi(value)
		}
	}
	return state
}

// Status records the outcome of the most recent `glab govern audit sync --all` run.
type Status struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Paused     []string  `json:"paused,omitempty"`
	// GovernanceNotEnabled lists the projects, as host/path, whose sessions
	// were not synced because GitLab has AI agent governance turned off for
	// them. These are expected for a user who works in many projects, so they
	// are not errors.
	GovernanceNotEnabled []string `json:"governance_not_enabled,omitempty"`
	SessionsSynced       int      `json:"sessions_synced"`
	SessionsCompleted    int      `json:"sessions_completed"`
	EventsPosted         int      `json:"events_posted"`
	Errors               []string `json:"errors,omitempty"`
}

// ErrNoStatus is returned by ReadStatus when no run has been recorded.
var ErrNoStatus = errors.New("no fallback sync run recorded")

func statusPath() string {
	return filepath.Join(config.ConfigDir(), "gaig", "fallback-sync-status.json")
}

// WriteStatus replaces the recorded status of the last run.
func WriteStatus(s Status) error {
	path := statusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s) //nolint:forbidigo // writing to disk, not stdout
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ReadStatus returns the recorded status of the last run.
func ReadStatus() (*Status, error) {
	data, err := os.ReadFile(statusPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoStatus
	}
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("could not parse fallback sync status: %w", err)
	}
	return &s, nil
}
