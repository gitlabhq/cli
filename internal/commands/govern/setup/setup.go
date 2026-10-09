package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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

			The fallback periodic sync runs %[1]sglab govern audit sync --all%[1]s every 30 minutes, and on Linux also 5 minutes after boot, until you remove it. It uploads anything the hooks missed for sessions they have already recorded, using your stored glab credentials and the glab configuration directory in use when you run setup. Each session goes to the project and host of the Git repository the agent ran in. It also marks sessions completed once they have been idle for 24 hours. It pauses Claude Code sessions while the hooks are not installed. Pass %[1]s--no-fallback-sync%[1]s to install only the hooks. The fallback periodic sync is not available on Windows.

			Safe to run multiple times: existing hooks are not duplicated, and an existing fallback periodic sync job is replaced. Run %[1]sglab govern doctor%[1]s afterwards to verify the setup.

			Run %[1]sglab govern setup --uninstall%[1]s to remove the fallback periodic sync job. To remove the hooks, delete the %[1]sglab govern audit sync%[1]s entries from %[1]s~/.claude/settings.json%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Configure hooks and the fallback periodic sync
			$ glab govern setup

			# Configure only the hooks
			$ glab govern setup --no-fallback-sync

			# Remove the fallback periodic sync
			$ glab govern setup --uninstall
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.uninstall {
				return runUninstall(cmd.Context(), opts)
			}
			return runSetup(cmd.Context(), opts)
		},
	}

	fl := cmd.Flags()
	fl.BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt.")
	fl.BoolVar(&opts.noFallbackSync, "no-fallback-sync", false, "Install only the hooks, without the fallback periodic sync job.")
	fl.BoolVar(&opts.uninstall, "uninstall", false, "Remove the fallback periodic sync job. The hooks are left in place.")
	cmd.MarkFlagsMutuallyExclusive("uninstall", "no-fallback-sync")

	return cmd
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

	prompt := "glab will install Claude Code hooks in ~/.claude/settings.json"
	if !opts.noFallbackSync {
		prompt += fmt.Sprintf(" and %s that syncs sessions in the background every %d minutes, replacing any existing job", fallbacksync.Description(opts.goos), int(fallbacksync.Interval.Minutes()))
	}
	confirmed, err := confirm(ctx, opts, prompt+". Do you wish to continue?")
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

	io.LogInfo("\nSetup complete. Run 'glab govern doctor' to verify.")
	return nil
}

func runUninstall(ctx context.Context, opts *options) error {
	io := opts.io
	c := io.Color()

	confirmed, err := confirm(ctx, opts, "glab will remove the fallback periodic sync job. Do you wish to continue?")
	if err != nil {
		return err
	}
	if !confirmed {
		io.LogInfo("Uninstall cancelled.")
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	removed, err := fallbacksync.Uninstall(ctx, opts.executor, opts.goos, home, os.Getuid())
	switch {
	case errors.Is(err, fallbacksync.ErrUnsupportedOS):
		io.LogInfo("No fallback periodic sync job found.")
	case err != nil:
		return fmt.Errorf("failed to remove fallback periodic sync: %w", err)
	case removed:
		io.LogInfof("%s Fallback periodic sync removed.\n", c.GreenCheck())
	default:
		io.LogInfo("No fallback periodic sync job found.")
	}
	return nil
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

	hooksJSON, err := json.Marshal(hooks) //nolint:forbidigo // marshaling for embedding in raw map
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
