package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io                   *iostreams.IOStreams
	yes                  bool
	settingsPathOverride string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io: f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure this machine to record AI agent sessions. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
		    Configure the current machine for AI agent governance session capture.

		    Installs the following:

		    - Stop hook in %[1]s~/.claude/settings.json%[1]s
		    - SessionEnd hook in %[1]s~/.claude/settings.json%[1]s

		    The Stop and SessionEnd hooks are the sync mechanism. Safe to run multiple times — existing hooks are not duplicated. Run %[1]sglab govern doctor%[1]s afterwards to verify the setup.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Configure hooks for AI agent governance
			$ glab govern setup
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt.")

	return cmd
}

func runSetup(ctx context.Context, opts *options) error {
	io := opts.io
	c := io.Color()

	var confirmed bool
	switch {
	case opts.yes:
		confirmed = true
	case !io.CanPrompt():
		return fmt.Errorf("cannot prompt for confirmation in non-interactive mode; pass --yes to skip")
	default:
		if err := io.Confirm(
			ctx,
			&confirmed,
			"glab will configure hooks for AI agent governance. Do you wish to continue?",
		); err != nil {
			return err
		}
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

	io.LogInfo("\nSetup complete. Run 'glab govern doctor' to verify.")
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
