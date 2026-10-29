package setup

import (
	"context"
	"errors"
	"fmt"
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
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/opencodeplugin"
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
			Configure the current machine for AI governance session capture.

			Choose the agents to sync with %[1]s--agents%[1]s. Without it, setup syncs the agents set up by an earlier run, or Claude Code if there are none. For each agent, setup installs:

			- Claude Code (%[1]sclaude-code%[1]s): a Stop hook in %[1]s~/.claude/settings.json%[1]s, which syncs the session after every agent turn, and a SessionEnd hook, which syncs the session and marks it completed.
			- OpenCode (%[1]sopencode%[1]s), version 1.4 or later: a plugin in %[1]s~/.config/opencode/plugins/glab-govern.js%[1]s (under %[1]s$XDG_CONFIG_HOME%[1]s when it is set), which syncs the session every time it goes idle. OpenCode loads it for all your sessions, in every project. Later setups keep it up to date.
			- Codex (%[1]scodex%[1]s) and Cursor (%[1]scursor%[1]s): nothing. The fallback periodic sync finds their sessions by scanning their transcripts in %[1]s~/.codex/sessions%[1]s and %[1]s~/.cursor/projects%[1]s, including sessions from before you enabled them, and uploads those from repositories on GitLab hosts you are logged in to with glab. Their sessions are marked completed once they have been idle for 24 hours.

			Setup also installs the fallback periodic sync: a launchd agent (%[1]s~/Library/LaunchAgents/com.gitlab.glab-govern-audit-sync.plist%[1]s) on macOS, or a systemd user timer (%[1]s~/.config/systemd/user/glab-govern-audit-sync.timer%[1]s) on Linux.

			The fallback periodic sync runs %[1]sglab govern audit sync --all%[1]s every 30 minutes, and on Linux also 5 minutes after boot, until you remove it. It uploads anything the hooks missed for sessions they have already recorded (see %[1]sglab govern audit sync --help%[1]s for what is uploaded), using your stored glab credentials and the glab configuration directory in use when you run setup. Each session goes to the project and host of the Git repository the agent ran in. It also marks sessions completed once they have been idle for 24 hours. It pauses sessions from agents that use hooks while those hooks are not installed. Pass %[1]s--no-fallback-sync%[1]s to install only the hooks and plugin, which Codex and Cursor cannot be synced without. The fallback periodic sync is not available on Windows.

			Run setup again with %[1]s--agents%[1]s to add agents. Agents set up by an earlier run stay set up, so to stop syncing an agent, run %[1]s--uninstall%[1]s and then set up the agents you want. Every setup prompt names the agents that are synced.

			Safe to run multiple times: existing hooks are not duplicated, and an existing fallback periodic sync job is replaced. Run %[1]sglab govern doctor%[1]s afterwards to verify the setup.

			Run %[1]sglab govern setup --uninstall%[1]s to remove everything setup installed: the hooks, the OpenCode plugin, the fallback periodic sync job, and Codex and Cursor session syncing. It also forgets the agents you set up, so the next setup starts with Claude Code again. Other hooks and settings in %[1]s~/.claude/settings.json%[1]s, and other OpenCode plugins, are left unchanged.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Sync Claude Code sessions, or the agents set up earlier
			$ glab govern setup

			# Sync OpenCode sessions
			$ glab govern setup --agents opencode

			# Sync Claude Code, OpenCode, and Codex sessions
			$ glab govern setup --agents claude-code,opencode,codex

			# Install only the hooks and plugin, without the fallback periodic sync
			$ glab govern setup --no-fallback-sync

			# Remove everything setup installed
			$ glab govern setup --uninstall
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.agentsChanged = cmd.Flags().Changed("agents")
			if err := validateAgents(opts.agents, opts.agentsChanged, opts.noFallbackSync); err != nil {
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
	fl.BoolVar(&opts.noFallbackSync, "no-fallback-sync", false, "Install only the hooks and plugin, without the fallback periodic sync job.")
	fl.BoolVar(&opts.uninstall, "uninstall", false, "Remove the hooks, the OpenCode plugin, and the fallback periodic sync job, and stop syncing Codex and Cursor sessions.")
	fl.StringSliceVar(&opts.agents, "agents", nil, fmt.Sprintf("Agents to sync sessions from: %s. Added to the agents set up by an earlier run. The OpenCode plugin runs for all your OpenCode sessions. Multiple agents can be comma-separated or specified by repeating the flag.", strings.Join(allAgents(), ", ")))
	cmd.MarkFlagsMutuallyExclusive("uninstall", "no-fallback-sync")
	cmd.MarkFlagsMutuallyExclusive("agents", "uninstall")

	return cmd
}

// plan is what a setup run installs.
type plan struct {
	claudeHooks bool
	// openCodePlugin is the path to install the OpenCode plugin at, or empty.
	openCodePlugin string
	// discovered are the agents the fallback periodic sync discovers.
	discovered []string
}

func setupPrompt(goos string, noFallbackSync bool, p plan) string {
	var items []string
	if p.claudeHooks {
		items = append(items, "agent session hooks in ~/.claude/settings.json")
	}
	if p.openCodePlugin != "" {
		items = append(items, "an OpenCode plugin in "+opencodeplugin.Display(p.openCodePlugin)+" that runs for all your OpenCode sessions")
	}
	if !noFallbackSync {
		items = append(items, fmt.Sprintf("%s that syncs sessions in the background every %d minutes, replacing any existing job", fallbacksync.Description(goos), int(fallbacksync.Interval.Minutes())))
	}
	prompt := "glab will install " + joinAnd(items)
	prompt += ". Agent sessions will be uploaded to the GitLab project of the repository they run in, including your prompts and each tool call's arguments, such as commands and file edits"
	if !noFallbackSync && len(p.discovered) > 0 {
		prompt += fmt.Sprintf(". The job will also upload your %s sessions the same way, from the last 89 days and from now on", strings.Join(p.discovered, " and "))
	}
	return prompt + ". Do you wish to continue?"
}

func uninstallPrompt(hooks bool, openCodePlugin string, agents []string) string {
	var items []string
	if hooks {
		items = append(items, "the agent session hooks from ~/.claude/settings.json")
	}
	if openCodePlugin != "" {
		items = append(items, "the OpenCode plugin at "+opencodeplugin.Display(openCodePlugin))
	}
	items = append(items, "the fallback periodic sync job")
	prompt := "glab will remove " + joinAnd(items)
	if len(agents) > 0 {
		prompt += fmt.Sprintf(". It will also stop syncing %s sessions", strings.Join(agents, " and "))
	}
	return prompt + ". Do you wish to continue?"
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}

func allAgents() []string {
	return slices.Concat(fallbacksync.HookAgents, fallbacksync.DiscoverableAgents)
}

func validateAgents(agents []string, changed, noFallbackSync bool) error {
	if changed && len(agents) == 0 {
		return cmdutils.FlagError{Err: errors.New("--agents needs at least one agent; to remove everything setup installed, use --uninstall")}
	}
	for _, a := range agents {
		switch {
		case !slices.Contains(allAgents(), a):
			return cmdutils.FlagError{Err: fmt.Errorf("unsupported agent %q in --agents; use %s", a, strings.Join(allAgents(), ", "))}
		case noFallbackSync && slices.Contains(fallbacksync.DiscoverableAgents, a):
			return cmdutils.FlagError{Err: fmt.Errorf("%s sessions are only synced by the fallback periodic sync, so --agents %s cannot be used with --no-fallback-sync", a, a)}
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

	hookAgents, discovered, err := chosenAgents(opts)
	if err != nil {
		return err
	}
	p, err := setupPlan(opts, hookAgents, discovered)
	if err != nil {
		return err
	}

	confirmed, err := confirm(ctx, opts, setupPrompt(opts.goos, opts.noFallbackSync, p))
	if err != nil {
		return err
	}
	if !confirmed {
		io.LogInfo("Setup cancelled.")
		return nil
	}

	if p.claudeHooks {
		io.LogInfo("Installing agent session hooks...")
		if err := installClaudeHooks(opts); err != nil {
			return fmt.Errorf("failed to install agent session hooks: %w", err)
		}
		io.LogInfof("%s Hooks installed in ~/%s\n", c.GreenCheck(), claudehooks.SettingsFile)
	}
	if p.openCodePlugin != "" {
		if _, err := opencodeplugin.Install(p.openCodePlugin); err != nil {
			return fmt.Errorf("failed to install the OpenCode plugin: %w", err)
		}
		io.LogInfof("%s OpenCode plugin installed in %s\n", c.GreenCheck(), opencodeplugin.Display(p.openCodePlugin))
	}
	if err := fallbacksync.SetHookAgents(hookAgents); err != nil {
		return fmt.Errorf("failed to record which agents are synced: %w", err)
	}

	if !opts.noFallbackSync {
		if err := installFallbackSync(ctx, opts); err != nil {
			io.LogErrorf("warning: could not install fallback periodic sync: %v\nAny previous fallback periodic sync job has been removed. Run 'glab govern setup' again once this is fixed.\n", err)
		} else {
			io.LogInfof("%s Fallback periodic sync installed: %s.\n", c.GreenCheck(), fallbacksync.Description(opts.goos))
		}
	}

	if opts.agentsChanged {
		if err := fallbacksync.SetDiscoveredAgents(discovered); err != nil {
			return fmt.Errorf("failed to enable session discovery: %w", err)
		}
		if len(discovered) > 0 {
			io.LogInfof("%s Syncing sessions from: %s.\n", c.GreenCheck(), strings.Join(discovered, ", "))
		}
	}

	io.LogInfo("\nSetup complete. Run 'glab govern doctor' to verify.")
	return nil
}

// chosenAgents returns the agents to install a hook or plugin for, and the
// agents for the fallback periodic sync to discover: those set up by an
// earlier run, and those passed with --agents.
func chosenAgents(opts *options) ([]string, []string, error) {
	discovered, err := fallbacksync.DiscoveredAgents()
	if err != nil {
		return nil, nil, agentsReadError(err)
	}
	settings, err := settingsPath(opts)
	if err != nil {
		return nil, nil, err
	}
	hookAgents, err := fallbacksync.ResolvedHookAgents(settings)
	if err != nil {
		return nil, nil, agentsReadError(err)
	}
	if !opts.agentsChanged {
		return fallbacksync.DefaultHookAgents(hookAgents, discovered), discovered, nil
	}

	hookAgents, discovered = slices.Clone(hookAgents), slices.Clone(discovered)
	for _, a := range opts.agents {
		switch {
		case slices.Contains(hookAgents, a) || slices.Contains(discovered, a):
		case slices.Contains(fallbacksync.HookAgents, a):
			hookAgents = append(hookAgents, a)
		default:
			discovered = append(discovered, a)
		}
	}
	return hookAgents, discovered, nil
}

func agentsReadError(err error) error {
	return fmt.Errorf("could not tell which agents are set up: %w; %s", err, fallbacksync.AgentsReadFix(err))
}

// setupPlan works out what to install for the chosen agents.
func setupPlan(opts *options, hookAgents, discovered []string) (plan, error) {
	p := plan{
		claudeHooks: slices.Contains(hookAgents, "claude-code"),
		discovered:  discovered,
	}
	switch {
	case opts.noFallbackSync && len(hookAgents) == 0:
		return plan{}, cmdutils.FlagError{Err: fmt.Errorf("%s sessions are only synced by the fallback periodic sync, so they cannot be set up with --no-fallback-sync", strings.Join(discovered, " and "))}
	case len(discovered) > 0 && !fallbacksync.Supported(opts.goos):
		return plan{}, fmt.Errorf("%s sessions need the fallback periodic sync: %w", strings.Join(discovered, " and "), fallbacksync.ErrUnsupportedOS)
	case p.claudeHooks && opts.goos == "windows":
		return plan{}, errors.New("hook installation is not supported on Windows")
	}
	if !slices.Contains(hookAgents, "opencode") {
		return p, nil
	}
	if opts.goos == "windows" {
		return plan{}, errors.New("the OpenCode plugin is not supported on Windows")
	}
	path, err := opencodeplugin.Path()
	if err != nil {
		return plan{}, err
	}
	switch state, err := opencodeplugin.Check(path); {
	case err != nil:
		return plan{}, fmt.Errorf("could not read the OpenCode plugin: %w", err)
	case state == opencodeplugin.Foreign:
		return plan{}, opencodeplugin.ForeignError(path)
	}
	p.openCodePlugin = path
	return p, nil
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
	// An unreadable settings file must not block removing the rest, but the
	// user is told before confirming that its hooks will stay.
	hooksPresent, hooksErr := claudeHooksInstalled(opts)
	openCodePlugin, pluginErr := installedOpenCodePlugin()
	if !jobPresent && !hooksPresent && openCodePlugin == "" && len(agents) == 0 && !unreadable {
		if hooksErr != nil || pluginErr != nil {
			return errors.Join(uninstallReadErrors(hooksErr, pluginErr)...)
		}
		forgetHookAgents()
		io.LogInfo("Nothing to uninstall: no hooks, OpenCode plugin, or fallback periodic sync job found.")
		return nil
	}
	if hooksErr != nil {
		io.LogErrorf("warning: could not read ~/%s, so its hooks will not be removed: %v\n", claudehooks.SettingsFile, hooksErr)
	}
	if pluginErr != nil {
		io.LogErrorf("warning: could not check for the OpenCode plugin, so it will not be removed: %v\n", pluginErr)
	}

	confirmed, err := confirm(ctx, opts, uninstallPrompt(hooksPresent, openCodePlugin, agents))
	if err != nil {
		return err
	}
	if !confirmed {
		io.LogInfo("Uninstall cancelled.")
		return nil
	}
	forgetHookAgents()

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

	if openCodePlugin != "" {
		removed, err := opencodeplugin.Remove(openCodePlugin)
		switch {
		case err != nil:
			return fmt.Errorf("failed to remove the OpenCode plugin: %w", err)
		case removed:
			io.LogInfof("%s OpenCode plugin removed from %s\n", c.GreenCheck(), opencodeplugin.Display(openCodePlugin))
		default:
			io.LogInfo("No OpenCode plugin found.")
		}
	}

	if jobPresent {
		removed, err := fallbacksync.Uninstall(ctx, opts.executor, opts.goos, home, os.Getuid())
		switch {
		case err != nil:
			return fmt.Errorf("failed to remove fallback periodic sync: %w", err)
		case removed:
			io.LogInfof("%s Fallback periodic sync removed.\n", c.GreenCheck())
		default:
			io.LogInfo("No fallback periodic sync job found.")
		}
	}

	if hooksPresent {
		removed, err := removeClaudeHooks(opts)
		switch {
		case err != nil:
			return fmt.Errorf("failed to remove agent session hooks: %w", err)
		case removed:
			io.LogInfof("%s Hooks removed from ~/%s\n", c.GreenCheck(), claudehooks.SettingsFile)
		default:
			io.LogInfo("No agent session hooks found.")
		}
	}
	return errors.Join(uninstallReadErrors(hooksErr, pluginErr)...)
}

// uninstallReadErrors reports what uninstall left in place because it could
// not be read.
func uninstallReadErrors(hooksErr, pluginErr error) []error {
	var errs []error
	if hooksErr != nil {
		errs = append(errs, fmt.Errorf("the agent session hooks were not removed; fix ~/%s and run 'glab govern setup --uninstall' again: %w", claudehooks.SettingsFile, hooksErr))
	}
	if pluginErr != nil {
		errs = append(errs, fmt.Errorf("the OpenCode plugin may still be installed; fix it and run 'glab govern setup --uninstall' again: %w", pluginErr))
	}
	return errs
}

// forgetHookAgents forgets the agents chosen by an earlier setup, so that a
// later setup starts from the default agents again.
func forgetHookAgents() {
	if err := fallbacksync.ForgetHookAgents(); err != nil {
		dbg.Debugf("could not forget which agents are synced: %v", err)
	}
}

// installedOpenCodePlugin returns the path of the OpenCode plugin glab
// installed, or an empty string when there is none.
func installedOpenCodePlugin() (string, error) {
	path, err := opencodeplugin.Path()
	if err != nil {
		return "", err
	}
	state, err := opencodeplugin.Check(path)
	if err != nil || state == opencodeplugin.NotInstalled || state == opencodeplugin.Foreign {
		return "", err
	}
	return path, nil
}

func claudeHooksInstalled(opts *options) (bool, error) {
	path, err := settingsPath(opts)
	if err != nil {
		return false, err
	}
	raw, err := claudehooks.LoadRawSettings(path)
	if err != nil {
		return false, err
	}
	hooks, err := claudehooks.HooksFromRaw(raw)
	if err != nil {
		return false, err
	}
	return claudehooks.HookPresent(hooks, "Stop", claudehooks.StopHookCommand) ||
		claudehooks.HookPresent(hooks, "SessionEnd", claudehooks.SessionEndHookCommand), nil
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

func removeClaudeHooks(opts *options) (bool, error) {
	path, err := settingsPath(opts)
	if err != nil {
		return false, err
	}

	raw, err := claudehooks.LoadRawSettings(path)
	if err != nil {
		return false, err
	}

	hooks, err := claudehooks.HooksFromRaw(raw)
	if err != nil {
		return false, err
	}

	changed := claudehooks.RemoveHook(hooks, "Stop", claudehooks.StopHookCommand)
	changed = claudehooks.RemoveHook(hooks, "SessionEnd", claudehooks.SessionEndHookCommand) || changed
	if !changed {
		return false, nil
	}

	if len(hooks) == 0 {
		delete(raw, "hooks")
		return true, claudehooks.WriteSettings(path, raw)
	}

	hooksJSON, err := claudehooks.MarshalHooks(hooks)
	if err != nil {
		return false, fmt.Errorf("could not serialise hooks: %w", err)
	}
	raw["hooks"] = hooksJSON

	return true, claudehooks.WriteSettings(path, raw)
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
	want := fallbacksync.BuildVersion(running)
	onPath, err := executor.LookPath("glab")
	if err != nil {
		dbg.Debugf("glab is not in PATH, scheduling the running glab: %v", err)
	} else {
		got, err := fallbacksync.BinaryVersion(ctx, executor, onPath)
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
