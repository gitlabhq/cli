package cli

import (
	"context"
	"errors"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/binarymgr/binaries"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	duoCLIUpdateCmd "gitlab.com/gitlab-org/cli/internal/commands/duo/cli/update"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// AppendBinaryStatusFooter registers a help function on cmd that renders glab's
// standard help and then appends a section describing how to reach the GitLab
// Duo CLI's own help, depending on whether the binary is installed.
func AppendBinaryStatusFooter(cmd *cobra.Command, f cmdutils.Factory) {
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		c.Root().HelpFunc()(c, args)
		// Subcommands inherit this help func, but the footer only describes cmd.
		if c != cmd {
			return
		}

		io := f.IO()
		io.LogInfo(io.Color().Bold("GITLAB DUO CLI"))

		path, version, installed := binaryStatus(f.Config())
		if installed {
			io.LogInfo(utils.Indent(heredoc.Docf(`
			  Installed: %s (%s)
			  Run 'glab duo cli help' for the GitLab Duo CLI commands and flags.`, version, path), "  "))
			return
		}

		io.LogInfo(utils.Indent(heredoc.Doc(`
		  The GitLab Duo CLI binary is not installed yet. To get started:

		    1. glab auth login          # authenticate, once
		    2. glab duo cli --install   # download the GitLab Duo CLI binary
		    3. glab duo cli             # start an interactive session

		  Or run 'glab duo cli' and follow the prompts.
		  After it is installed, 'glab duo cli help' shows the GitLab Duo CLI
		  commands and flags.`), "  "))
	})
}

func binaryStatus(cfg config.Config) (string, string, bool) {
	status, err := binarymgr.InstalledBinary(cfg, binaries.DuoCLI())
	if err != nil {
		dbg.Debugf("binaryStatus: %v", err)
	}
	return status.Path, status.Version, status.Installed
}

// NewCmd creates the `glab duo cli` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	spec := binaries.DuoCLI()
	cmd := &cobra.Command{
		Use:   "cli [command]",
		Short: "Run the GitLab Duo CLI.",
		Long: heredoc.Docf(`
			Run the GitLab Duo CLI (%[1]sduo%[1]s) through %[1]sglab%[1]s.

			The GitLab Duo CLI brings the GitLab Duo Agent Platform to your terminal. Ask GitLab Duo questions about your codebase and use it to autonomously perform actions on your behalf.

			The GitLab Duo CLI runs in two modes:

			- Interactive (default): %[1]sglab duo cli%[1]s opens a session for multiple prompts, with build and plan modes.
			- Headless: %[1]sglab duo cli run --goal "<prompt>"%[1]s runs a single prompt and exits. For use in runners, scripts, and automated workflows.

			When you use the GitLab Duo CLI through %[1]sglab%[1]s, %[1]sglab%[1]s handles authentication for you. You authenticate only once.

			Prerequisites:

			- Use GitLab 19.2 or later.
			- Run %[1]sglab auth login%[1]s to authenticate.
			- Meet the [prerequisites for GitLab Duo Agent Platform](https://docs.gitlab.com/user/duo_agent_platform/#prerequisites).
			- Set a [default GitLab Duo namespace](https://docs.gitlab.com/user/profile/preferences/#namespace-resolution-in-your-local-environment), or run the command in a project that has GitLab Duo access.
			- For GitLab Self-Managed and GitLab Dedicated on 19.2 or later, turn on [GitLab Duo CLI access](https://docs.gitlab.com/user/gitlab_duo_cli/#manage-gitlab-duo-cli-access). It is on by default.

			If you are on GitLab 18.11 to 19.1, you can use the GitLab Duo CLI by turning on [beta and experimental features](https://docs.gitlab.com/user/duo_agent_platform/turn_on_off/#turn-on-beta-and-experimental-features).

			Configuration options:

			- %[1]sduo_cli_auto_run%[1]s: Skip the run confirmation prompt.
			- %[1]sduo_cli_auto_download%[1]s: Skip the download confirmation prompt.

			Except for the %[1]supdate%[1]s command, %[1]sglab%[1]s passes all other arguments and flags through to the GitLab Duo CLI binary. To see the GitLab Duo CLI commands and flags, run %[1]sglab duo cli help%[1]s.

			For more information, see the [GitLab Duo CLI documentation](https://docs.gitlab.com/user/gitlab_duo_cli/).
		`, "`"),
		Annotations: map[string]string{
			"help:environment": heredoc.Docf(`
				- %[1]sGLAB_DUO_CLI_BINARY_PATH%[1]s: Use a local binary instead of the managed one.
				  Skips download, version checks, and updates. Can also be set through the
				  %[1]sduo_cli_binary_path%[1]s configuration key.
				`, "`"),
			mcpannotations.Exclude: "true",
		},
		Example: heredoc.Doc(`
			# Start an interactive GitLab Duo CLI session
			glab duo cli

			# Use the GitLab Duo CLI in headless mode with a single prompt
			glab duo cli run --goal "Fix the failing tests in this project"

			# Pass any command or flag through to the GitLab Duo CLI binary (for example: model, version, run)
			glab duo cli <command>
			glab duo cli --model claude_sonnet_4_6

			# Show GitLab CLI help content
			glab duo cli --help

			# Show GitLab Duo CLI help content, including commands and flags
			glab duo cli help

			# Run without prompts (for use in scripts and non-interactive environments)
			glab duo cli --yes

			# Install the GitLab Duo CLI binary
			glab duo cli --install

			# Install the GitLab Duo CLI binary without prompts
			glab duo cli --install --yes

			# Check for and install updates
			glab duo cli update`),
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runner := newRunner(f.IO(), f.Config(), spec)

			// DisableFlagParsing is on, so split glab-owned flags from
			// pass-through args manually. --help/-h shows glab's help only
			// when nothing has been collected for pass-through yet.
			var remaining []string
			for _, arg := range args {
				switch arg {
				case "--update":
					runner.Update = true
				case "--install":
					runner.Install = true
				case "--yes", "-y":
					runner.Yes = true
				case "--help", "-h":
					if len(remaining) == 0 {
						return cmd.Help()
					}
					remaining = append(remaining, arg)
				default:
					remaining = append(remaining, arg)
				}
			}
			runner.Args = remaining

			if runner.Install && runner.Update {
				return errors.New("the --install and --update flags are mutually exclusive")
			}

			warnIfSnapConfined(f.IO(), runner.Install, runner.Update)

			if runner.Install {
				return runner.HandleInstall(cmd.Context())
			}
			return runner.Run(cmd.Context())
		},
	}

	// Registered for documentation only — DisableFlagParsing means Cobra
	// never parses these; the RunE switch above handles them manually.
	fl := cmd.Flags()
	fl.BoolP("yes", "y", false, "Skip confirmation prompts.")
	fl.Bool("install", false, "Install the GitLab Duo CLI binary without running it.")
	fl.Bool("update", false, "Check for and install updates to the binary. Same as the update command.")

	cmd.AddCommand(duoCLIUpdateCmd.NewCmd(f))
	AppendBinaryStatusFooter(cmd, f)

	return cmd
}

// warnIfSnapConfined warns when `glab duo cli` runs under snap confinement:
// the sandbox blocks the credential-helper callback the Duo binary needs, so
// authentication fails even after a successful `glab auth login`. --install
// and --update skip this warning since they don't hit the callback.
func warnIfSnapConfined(io *iostreams.IOStreams, install, update bool) {
	if install || update {
		return
	}
	if !config.SnapConfined() {
		return
	}
	msg := heredoc.Docf(`
		%[1]s glab is running under snap confinement.

		The GitLab Duo CLI authenticates by spawning %[2]sglab auth credential-helper%[2]s, but
		snap's sandbox blocks that callback. Authentication is likely to fail with
		"no credentials found" even after a successful %[2]sglab auth login%[2]s.

		To use %[2]sglab duo cli%[2]s, install glab from a non-snap source:
		  - Homebrew:     %[2]sbrew install glab%[2]s
		  - mise:         %[2]smise use -g glab@latest%[2]s
		  - Native binary: https://gitlab.com/gitlab-org/cli/-/releases

		Or set the GITLAB_TOKEN environment variable (%[2]sGITLAB_TOKEN=glpat-xyz glab duo cli%[2]s).
		For GitLab Self-Managed, also set GITLAB_URL (%[2]sGITLAB_URL=https://gitlab.example.com GITLAB_TOKEN=glpat-xyz glab duo cli%[2]s).

	`, io.Color().DotWarnIcon(), "`")
	io.LogError(msg)
}

// newRunner builds a binarymgr.Runner for the Duo CLI. The Executor is
// platform-specific: syscall.Exec on Unix (in execute_unix.go), subprocess
// on Windows (in execute_windows.go). Each declares executeDuoCLI(io, ...).
func newRunner(io *iostreams.IOStreams, cfg config.Config, spec binarymgr.Spec) *binarymgr.Runner {
	return &binarymgr.Runner{
		IO:      io,
		Cfg:     cfg,
		Spec:    spec,
		Manager: binarymgr.NewManager(io, spec),
		Executor: func(ctx context.Context, binaryPath string, args []string) error {
			return executeDuoCLI(ctx, io, binaryPath, args)
		},
	}
}
