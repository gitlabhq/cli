package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/version"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io                   *iostreams.IOStreams
	executor             cmdutils.Executor
	buildInfo            api.BuildInfo
	goos                 string
	yes                  bool
	noFallbackSync       bool
	agents               []string
	agentsChanged        bool
	uninstall            bool
	settingsPathOverride string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		executor:  f.Executor(),
		buildInfo: f.BuildInfo(),
		goos:      runtime.GOOS,
	}

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure this machine to record AI agent sessions. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Configure the current machine for AI agent governance session capture.

			Installs the following:

			- Stop hook in %[1]s~/.claude/settings.json%[1]s, which syncs the session after every agent turn.
			- SessionEnd hook in %[1]s~/.claude/settings.json%[1]s, which syncs the session and marks it completed.
			- Fallback periodic sync: a launchd agent (%[1]s~/Library/LaunchAgents/com.gitlab.glab-govern-audit-sync.plist%[1]s) on macOS, or a systemd user timer (%[1]s~/.config/systemd/user/glab-govern-audit-sync.timer%[1]s) on Linux.

			The fallback periodic sync runs %[1]sglab govern audit sync --all%[1]s every 30 minutes, and on Linux also 5 minutes after boot, until you remove it. It uploads anything the hooks missed for sessions they have already recorded (see %[1]sglab govern audit sync --help%[1]s for what is uploaded), using your stored glab credentials and the glab configuration directory in use when you run setup. Each session goes to the project and host of the Git repository the agent ran in. It also marks sessions completed once they have been idle for 24 hours. It pauses Claude Code sessions while the hooks are not installed. Pass %[1]s--no-fallback-sync%[1]s to install only the hooks. The fallback periodic sync is not available on Windows.

			Pass %[1]s--agents codex,cursor%[1]s to also sync Codex and Cursor sessions. glab installs no hooks for those agents: the fallback periodic sync finds their sessions by scanning their transcripts in %[1]s~/.codex/sessions%[1]s and %[1]s~/.cursor/projects%[1]s, including sessions from before you enabled them, and uploads those from repositories on GitLab hosts you are logged in to with glab. Their sessions are marked completed once they have been idle for 24 hours. Run setup again with a different %[1]s--agents%[1]s list to change which agents are synced, or with %[1]s--agents ""%[1]s to stop. Every setup prompt names the agents that are synced, and %[1]s--uninstall%[1]s also stops syncing them.

			Safe to run multiple times: existing hooks are not duplicated, and an existing fallback periodic sync job is replaced. Run %[1]sglab govern doctor%[1]s afterwards to verify the setup.

			Run %[1]sglab govern setup --uninstall%[1]s to remove the fallback periodic sync job. To remove the hooks, delete the %[1]sglab govern audit sync%[1]s entries from %[1]s~/.claude/settings.json%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Configure hooks and the fallback periodic sync
			$ glab govern setup

			# Configure only the hooks
			$ glab govern setup --no-fallback-sync

			# Also sync Codex and Cursor sessions through the fallback periodic sync
			$ glab govern setup --agents codex,cursor

			# Remove the fallback periodic sync
			$ glab govern setup --uninstall
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.agentsChanged = cmd.Flags().Changed("agents")
			if err := validateAgents(opts.agents); err != nil {
				return err
			}
			if opts.uninstall {
				return runUninstall(cmd.Context(), opts)
			}
			return runSetup(cmd.Context(), opts)
		},
	}

	fl := cmd.Flags()
	fl.BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt.")
	fl.BoolVar(&opts.noFallbackSync, "no-fallback-sync", false, "Install only the hooks, without the fallback periodic sync job.")
	fl.BoolVar(&opts.uninstall, "uninstall", false, "Remove the fallback periodic sync job and stop syncing Codex and Cursor sessions. The hooks are left in place.")
	fl.StringSliceVar(&opts.agents, "agents", nil, fmt.Sprintf("Also sync sessions from these agents, found by the fallback periodic sync without hooks: %s. Replaces the agents enabled by an earlier run. Multiple agents can be comma-separated or specified by repeating the flag.", strings.Join(fallbacksync.DiscoverableAgents, ", ")))
	cmd.MarkFlagsMutuallyExclusive("uninstall", "no-fallback-sync")
	cmd.MarkFlagsMutuallyExclusive("agents", "no-fallback-sync")
	cmd.MarkFlagsMutuallyExclusive("agents", "uninstall")

	return cmd
}

func setupPrompt(goos string, noFallbackSync bool, agents []string) string {
	prompt := "glab will install Claude Code hooks in ~/.claude/settings.json"
	if !noFallbackSync {
		prompt += fmt.Sprintf(" and %s that syncs sessions in the background every %d minutes, replacing any existing job", fallbacksync.Description(goos), int(fallbacksync.Interval.Minutes()))
	}
	prompt += ". Claude Code sessions will be uploaded to the GitLab project of the repository they run in, including your prompts and each tool call's arguments, such as commands and file edits"
	if !noFallbackSync && len(agents) > 0 {
		prompt += fmt.Sprintf(". The job will also upload your %s sessions the same way, from the last 89 days and from now on", strings.Join(agents, " and "))
	}
	return prompt + ". Do you wish to continue?"
}

func uninstallPrompt(agents []string) string {
	prompt := "glab will remove the fallback periodic sync job"
	if len(agents) > 0 {
		prompt += fmt.Sprintf(" and stop syncing %s sessions", strings.Join(agents, " and "))
	}
	return prompt + ". Do you wish to continue?"
}

func validateAgents(agents []string) error {
	for _, a := range agents {
		if !slices.Contains(fallbacksync.DiscoverableAgents, a) {
			return cmdutils.FlagError{Err: fmt.Errorf("unsupported agent %q in --agents; use %s", a, strings.Join(fallbacksync.DiscoverableAgents, ", "))}
		}
	}
	return nil
}

func confirm(ctx context.Context, opts *options, prompt string) (bool, error) {
	io := opts.io
	switch {
	case opts.yes:
		return true, nil
	case !io.CanPrompt():
		return false, fmt.Errorf("cannot prompt for confirmation in non-interactive mode; pass --yes to skip")
	}
	var confirmed bool
	if err := io.Confirm(ctx, &confirmed, prompt); err != nil {
		return false, err
	}
	return confirmed, nil
}

func runSetup(ctx context.Context, opts *options) error {
	io := opts.io
	c := io.Color()

	// The agents already enabled are named too, because re-running setup
	// brings their discovery back with the job.
	agents := opts.agents
	if !opts.agentsChanged {
		enabled, err := fallbacksync.DiscoveredAgents()
		if err != nil {
			return fmt.Errorf("could not read which agents are synced: %w", err)
		}
		agents = enabled
	}
	confirmed, err := confirm(ctx, opts, setupPrompt(opts.goos, opts.noFallbackSync, agents))
	if err != nil {
		return err
	}
	if !confirmed {
		io.LogInfo("Setup cancelled.")
		return nil
	}

	io.LogInfo("Installing Claude Code hooks...")
	if err := installClaudeHooks(opts); err != nil {
		return fmt.Errorf("failed to install Claude Code hooks: %w", err)
	}
	io.LogInfof("%s Hooks installed in ~/%s\n", c.GreenCheck(), claudehooks.SettingsFile)

	if !opts.noFallbackSync {
		if err := installFallbackSync(ctx, opts); err != nil {
			io.LogErrorf("warning: could not install fallback periodic sync: %v\nAny previous fallback periodic sync job has been removed. Run 'glab govern setup' again once this is fixed.\n", err)
		} else {
			io.LogInfof("%s Fallback periodic sync installed: %s.\n", c.GreenCheck(), fallbacksync.Description(opts.goos))
		}
	}

	if opts.agentsChanged {
		if err := fallbacksync.SetDiscoveredAgents(opts.agents); err != nil {
			return fmt.Errorf("failed to enable session discovery: %w", err)
		}
		if len(opts.agents) > 0 {
			io.LogInfof("%s Syncing sessions from: %s.\n", c.GreenCheck(), strings.Join(opts.agents, ", "))
		} else {
			io.LogInfo("Session discovery turned off.")
		}
	}

	io.LogInfo("\nSetup complete. Run 'glab govern doctor' to verify.")
	return nil
}

func runUninstall(ctx context.Context, opts *options) error {
	io := opts.io
	c := io.Color()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// An unreadable list must not block uninstall, which is the way to
	// recover from it. It is reset with the rest.
	agents, err := fallbacksync.DiscoveredAgents()
	unreadable := err != nil
	if unreadable {
		dbg.Debugf("could not read which agents are synced: %v", err)
	}
	jobPresent := jobMayBePresent(ctx, opts, home)
	if !jobPresent && len(agents) == 0 && !unreadable {
		io.LogInfo("No fallback periodic sync job found.")
		return nil
	}

	confirmed, err := confirm(ctx, opts, uninstallPrompt(agents))
	if err != nil {
		return err
	}
	if !confirmed {
		io.LogInfo("Uninstall cancelled.")
		return nil
	}

	// Discovery is turned off too, so a later setup does not bring it back
	// without the user choosing it again.
	if len(agents) > 0 || unreadable {
		if err := fallbacksync.SetDiscoveredAgents(nil); err != nil {
			return fmt.Errorf("failed to turn off session discovery: %w", err)
		}
	}
	if len(agents) > 0 {
		io.LogInfof("%s Stopped syncing %s sessions.\n", c.GreenCheck(), strings.Join(agents, ", "))
	}

	if !jobPresent {
		return nil
	}
	removed, err := fallbacksync.Uninstall(ctx, opts.executor, opts.goos, home, os.Getuid())
	switch {
	case err != nil:
		return fmt.Errorf("failed to remove fallback periodic sync: %w", err)
	case removed:
		io.LogInfof("%s Fallback periodic sync removed.\n", c.GreenCheck())
	default:
		io.LogInfo("No fallback periodic sync job found.")
	}
	return nil
}

// jobMayBePresent reports whether uninstall has anything to remove: the job's
// files, or a job still loaded after its files were deleted. When either
// check fails, it errs towards true so that uninstall still runs.
func jobMayBePresent(ctx context.Context, opts *options, home string) bool {
	_, err := fallbacksync.InstalledBinary(opts.goos, home)
	switch {
	case errors.Is(err, fallbacksync.ErrUnsupportedOS):
		return false
	case !errors.Is(err, fallbacksync.ErrNotInstalled):
		return true
	}
	state, err := fallbacksync.State(ctx, opts.executor, opts.goos, os.Getuid())
	if err != nil {
		dbg.Debugf("could not check whether the fallback job is loaded: %v", err)
		return true
	}
	return state.Loaded
}

func settingsPath(opts *options) (string, error) {
	if opts != nil && opts.settingsPathOverride != "" {
		return opts.settingsPathOverride, nil
	}
	return claudehooks.SettingsPath()
}

func installClaudeHooks(opts *options) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("hook installation is not supported on Windows")
	}

	path, err := settingsPath(opts)
	if err != nil {
		return err
	}

	raw, err := claudehooks.LoadRawSettings(path)
	if err != nil {
		return err
	}

	hooks, err := claudehooks.HooksFromRaw(raw)
	if err != nil {
		return err
	}

	changed := false
	changed = claudehooks.AddHook(hooks, "Stop", claudehooks.StopHookCommand) || changed
	changed = claudehooks.AddHook(hooks, "SessionEnd", claudehooks.SessionEndHookCommand) || changed

	if !changed {
		return nil
	}

	hooksJSON, err := claudehooks.MarshalHooks(hooks)
	if err != nil {
		return fmt.Errorf("could not serialise hooks: %w", err)
	}
	raw["hooks"] = hooksJSON

	return claudehooks.WriteSettings(path, raw)
}

func installFallbackSync(ctx context.Context, opts *options) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	glabPath, err := glabBinaryPath(ctx, opts.executor, opts.buildInfo)
	if err != nil {
		return err
	}
	job := fallbacksync.Job{GlabPath: glabPath, ConfigDir: config.ConfigDir()}
	return fallbacksync.Install(ctx, opts.executor, opts.goos, home, job, os.Getuid())
}

// glabBinaryPath returns the path the scheduled job should run: the glab on
// PATH if it is the same build as the running glab, otherwise the running
// glab. Symlinks are deliberately left unresolved: package managers such as
// Homebrew, asdf, and mise point a stable path (or shim) at a versioned
// install directory that is deleted on upgrade. Builds are compared by
// version rather than by file, because a shim is a different file from the
// binary it runs.
func glabBinaryPath(ctx context.Context, executor cmdutils.Executor, running api.BuildInfo) (string, error) {
	want := strings.TrimSpace(version.Scheme(running.Version, running.Commit))
	onPath, err := executor.LookPath("glab")
	if err != nil {
		dbg.Debugf("glab is not in PATH, scheduling the running glab: %v", err)
	} else {
		var stdout bytes.Buffer
		err := executor.ExecWithIO(ctx, onPath, []string{"version"}, nil, nil, &stdout, io.Discard)
		got := strings.TrimSpace(stdout.String())
		switch {
		case err != nil:
			dbg.Debugf("could not run %s version, scheduling the running glab: %v", onPath, err)
		case got != want:
			dbg.Debugf("%s reports %q, not %q, scheduling the running glab", onPath, got, want)
		default:
			return filepath.Abs(onPath)
		}
	}
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not find glab binary: %w", err)
	}
	return filepath.Abs(path)
}
