package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"syscall"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands"
	"gitlab.com/gitlab-org/cli/internal/commands/alias/expand"
	"gitlab.com/gitlab-org/cli/internal/commands/help"
	"gitlab.com/gitlab-org/cli/internal/commands/update"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/text"
	"gitlab.com/gitlab-org/cli/internal/theme"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var (
	// version is set dynamically at build
	version = "DEV"
	// commit is set dynamically at build
	commit string
	// platform is set dynamically at build
	platform = runtime.GOOS

	// debugMode is set dynamically at build and can be overridden by
	// the configuration file or environment variable
	// sets to "true" or "false" or "1" or "0" as string
	debugMode = "false"
)

// gitLabColorScheme returns a custom color scheme using GitLab product colors
// for semantic meaning and accessibility across different terminal backgrounds.
// The theme is defined in internal/theme/gitlab.go for reuse across Charm libraries.
func gitLabColorScheme(lightDarkFunc lipgloss.LightDarkFunc) fang.ColorScheme {
	return theme.FangColorScheme(lightDarkFunc)
}

func main() {
	cfg, err := config.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read configuration:  %s\n", err)
		os.Exit(2)
	}

	// Set Debug mode from config if not previously set by debugMode
	debug := debugMode == "true" || debugMode == "1"
	if !debug {
		debugModeCfg, _ := cfg.Get("", "debug")
		debug = debugModeCfg == "true" || debugModeCfg == "1"
	}

	// Initialize factory and iostreams
	cmdFactory := cmdutils.NewFactory(
		iostreams.New(
			iostreams.WithStdin(os.Stdin, iostreams.IsTerminal(os.Stdin)),
			iostreams.WithStdout(iostreams.NewColorable(os.Stdout), iostreams.IsTerminal(os.Stdout)),
			iostreams.WithStderr(iostreams.NewColorable(os.Stderr), iostreams.IsTerminal(os.Stderr)),
			iostreams.WithPagerCommand(iostreams.PagerCommandFromEnv()),

			// overwrite pager from env if set via config
			func(i *iostreams.IOStreams) {
				if pager, _ := cfg.Get("", "glab_pager"); pager != "" {
					i.SetPager(pager)
				}
			},

			// configure hyperlink display
			func(i *iostreams.IOStreams) {
				configureHyperlinks(i, cfg)
			},

			// configure color from the `color` key / GLAB_COLOR
			func(i *iostreams.IOStreams) {
				configureColorMode(i, cfg)
			},

			// configure prompt
			func(i *iostreams.IOStreams) {
				if value, found := utils.IsEnvVarEnabled("NO_PROMPT"); found {
					i.SetPrompt(strconv.FormatBool(value))
					return
				}

				if value, found := utils.IsEnvVarEnabled("GLAB_NO_PROMPT"); found {
					i.SetPrompt(strconv.FormatBool(value))
					return
				}

				if promptDisabled, _ := cfg.Get("", "no_prompt"); promptDisabled != "" {
					i.SetPrompt(promptDisabled)
				}
			},
		),
		true,
		cfg,
		api.BuildInfo{Version: version, Commit: commit, Platform: platform, Architecture: runtime.GOARCH, CodingAgent: api.DetectCodingAgent(), InvocationSource: api.DetectInvocationSource()},
	)

	// Setup command
	var expandedArgs []string
	if len(os.Args) > 0 {
		expandedArgs = os.Args[1:]
	}
	rootCmd := commands.NewCmdRoot(cmdFactory)
	cmd, _, err := rootCmd.Traverse(expandedArgs)

	if err != nil || cmd == rootCmd {
		originalArgs := expandedArgs
		isShell := false
		expandedArgs, isShell, err = expand.ExpandAlias(cfg, os.Args, nil)
		if err != nil {
			cmdFactory.IO().LogErrorf("Failed to process alias: %s\n", err)
			os.Exit(2)
		}

		if debug {
			fmt.Printf("%v -> %v\n", originalArgs, expandedArgs)
		}

		if isShell {
			externalCmd := exec.Command(expandedArgs[0], expandedArgs[1:]...)
			externalCmd.Stderr = os.Stderr
			externalCmd.Stdout = os.Stdout
			externalCmd.Stdin = os.Stdin
			preparedCmd := run.PrepareCmd(externalCmd)

			err = preparedCmd.Run()
			if err != nil {
				ee := &exec.ExitError{}
				if errors.As(err, &ee) {
					os.Exit(ee.ExitCode())
				}

				cmdFactory.IO().LogErrorf("failed to run external command: %s\n", err)
				os.Exit(3)
			}

			os.Exit(0)
		}
	}

	// Override the default column separator of tableprinter to double spaces
	tableprinter.SetTTYSeparator("  ")
	// Override the default terminal width of tableprinter
	tableprinter.SetTerminalWidth(cmdFactory.IO().TerminalWidth())
	// set whether terminal is a TTY or non-TTY
	tableprinter.SetIsTTY(cmdFactory.IO().IsOutputTTY())

	rootCmd.SetArgs(expandedArgs)
	setupTelemetryHook(cfg, cmdFactory, rootCmd, expandedArgs)

	// Convert markdown [text](url) links in command Long and Example fields to
	// OSC 8 terminal hyperlinks before Fang renders help text.
	preprocessCommandLinks(rootCmd, cmdFactory.IO())

	colorEnv := newFangColorEnv(cmdFactory.IO().ColorMode())
	colorEnv.apply()
	scopeColorEnvToFang(rootCmd, colorEnv)

	if err := fang.Execute(context.Background(), rootCmd,
		fang.WithoutCompletions(),
		fang.WithoutManpage(),
		fang.WithColorSchemeFunc(gitLabColorScheme),
		fang.WithErrorHandler(cmdutils.NewGitLabErrorHandler(cmdFactory.IO())),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	); err != nil {
		var exitError *cmdutils.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.Code)
		} else {
			os.Exit(1)
		}
	}

	if help.HasFailed() {
		os.Exit(1)
	}

	var argCommand string
	if expandedArgs != nil {
		argCommand = expandedArgs[0]
	}
	if !update.ShouldSkipUpdate(argCommand) {
		update.MaybeShowPostUpgradeBanner(cmdFactory.IO(), cmdFactory.Config(), cmdFactory.BuildInfo())
		checkForUpdate(cmdFactory, rootCmd, debug)
	}
}

func setupTelemetryHook(cfg config.Config, f cmdutils.Factory, rootCmd *cobra.Command, args []string) {
	if isTelemetryEnabled(cfg) {
		cobra.OnFinalize(addTelemetryHook(f, rootCmd, args))
	}
}

func checkForUpdate(f cmdutils.Factory, rootCmd *cobra.Command, debug bool) {
	if !isUpdateCheckEnabled(f) {
		return
	}

	if err := update.CheckUpdate(f, true); err != nil {
		update.PrintUpdateError(f.IO(), err, rootCmd, debug)
	}
}

func isUpdateCheckEnabled(f cmdutils.Factory) bool {
	if enabled, found := utils.IsEnvVarEnabled("GLAB_CHECK_UPDATE"); found {
		return enabled
	}

	val, err := f.Config().Get("", "check_update")
	// WARN: I return true here since I think we should always check for updates
	// and an error likely indicates that the value wasn't found in the config.
	if err != nil || val == "" {
		return true
	}

	checkUpdate, err := strconv.ParseBool(val)
	if err != nil {
		f.IO().LogErrorf("ERROR: Could not parse config value %q: %s", "check_update", err)
	}

	return checkUpdate
}

func preprocessCommandLinks(cmd *cobra.Command, io *iostreams.IOStreams) {
	if cmd.Long != "" {
		cmd.Long = text.ConvertMarkdownLinks(cmd.Long, io.Hyperlink)
	}
	if cmd.Example != "" {
		cmd.Example = text.ConvertMarkdownLinks(cmd.Example, io.Hyperlink)
	}
	for _, sub := range cmd.Commands() {
		preprocessCommandLinks(sub, io)
	}
}

// configureHyperlinks sets the IOStreams hyperlink mode based on
// FORCE_HYPERLINKS, GLAB_FORCE_HYPERLINKS, and the `display_hyperlinks`
// config value, in that order of precedence.
//
// A falsy env var value (`0`, `false`) falls through to the config check
// so that a user with `FORCE_HYPERLINKS=0` in their shell baseline can
// still opt out through `display_hyperlinks: false`.
func configureHyperlinks(i *iostreams.IOStreams, cfg config.Config) {
	if enabled, found := utils.IsEnvVarEnabled("FORCE_HYPERLINKS"); found && enabled {
		i.SetDisplayHyperlinks("always")
		return
	}

	if enabled, found := utils.IsEnvVarEnabled("GLAB_FORCE_HYPERLINKS"); found && enabled {
		i.SetDisplayHyperlinks("always")
		return
	}

	switch displayHyperlinks, _ := cfg.Get("", "display_hyperlinks"); displayHyperlinks {
	case "false":
		i.SetDisplayHyperlinks("never")
	case "true":
		i.SetDisplayHyperlinks("auto")
	}
}

// configureColorMode sets whether glab colors output from the `color` config
// key, which config.Get already resolves against GLAB_COLOR per the key's
// declared EnvVars (see internal/config/schema.go). An unrecognized value
// falls back to ColorModeAuto rather than failing the command, matching how
// glab treats other soft-fallback config values (e.g. an invalid theme).
func configureColorMode(i *iostreams.IOStreams, cfg config.Config) {
	value, _ := cfg.Get("", "color")
	mode := iostreams.ColorMode(value)
	if value != "" && !slices.Contains(iostreams.AllColorModes(), mode) {
		dbg.Debugf("color: %q is not a recognized value (want one of %v); falling back to auto", value, iostreams.AllColorModes())
		mode = iostreams.ColorModeAuto
	}
	i.SetColorMode(mode)
}

// fangColorEnv carries the color mode to fang, which reads its color level
// only from the process environment. CLICOLOR_FORCE beats NO_COLOR for
// colorprofile on a non-TTY, so each mode clears the variable it opposes.
type fangColorEnv struct {
	set     map[string]string
	unset   []string
	orig    map[string]string
	origSet map[string]bool
}

func newFangColorEnv(mode iostreams.ColorMode) *fangColorEnv {
	e := &fangColorEnv{orig: map[string]string{}, origSet: map[string]bool{}}
	switch mode {
	case iostreams.ColorModeNever:
		e.set = map[string]string{"NO_COLOR": "1"}
		e.unset = []string{"CLICOLOR_FORCE"}
	case iostreams.ColorModeAlways:
		e.set = map[string]string{"CLICOLOR_FORCE": "1"}
		e.unset = []string{"NO_COLOR"}
	}
	for _, k := range append(e.unset, keys(e.set)...) {
		e.orig[k], e.origSet[k] = os.LookupEnv(k)
	}
	return e
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (e *fangColorEnv) apply() {
	for _, k := range e.unset {
		setenv(k, "", false)
	}
	for k, v := range e.set {
		setenv(k, v, true)
	}
}

func (e *fangColorEnv) restore() {
	for k := range e.orig {
		setenv(k, e.orig[k], e.origSet[k])
	}
}

func setenv(key, value string, present bool) {
	var err error
	if present {
		err = os.Setenv(key, value)
	} else {
		err = os.Unsetenv(key)
	}
	if err != nil {
		dbg.Debugf("color: could not update %s for fang: %v", key, err)
	}
}

// scopeColorEnvToFang restores the original environment while a command runs,
// so children glab starts (git, pagers, editors, alias shells) don't inherit
// the variables meant for fang, and re-applies it afterwards for fang's error
// output.
func scopeColorEnvToFang(root *cobra.Command, e *fangColorEnv) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if run := c.Run; run != nil {
			c.Run = func(cmd *cobra.Command, args []string) {
				e.restore()
				defer e.apply()
				run(cmd, args)
			}
		}
		if runE := c.RunE; runE != nil {
			c.RunE = func(cmd *cobra.Command, args []string) error {
				e.restore()
				defer e.apply()
				return runE(cmd, args)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
