package minder

import (
	"context"
	"errors"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/binarymgr/binaries"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	governCmd "gitlab.com/gitlab-org/cli/internal/commands/govern"
	minderUpdateCmd "gitlab.com/gitlab-org/cli/internal/commands/minder/update"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io      *iostreams.IOStreams
	cfg     config.Config
	execute func(ctx context.Context, io *iostreams.IOStreams, binaryPath string, args []string) error
	help    func() error
	flags   binarymgr.Flags
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{io: f.IO(), cfg: f.Config(), execute: executeMinder}
	cmd := &cobra.Command{
		Use:   "minder [<command>] [flags]",
		Short: "Manage AI governance and run the GitLab Endpoint Agent Platform. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Manage AI governance for external agents and run the GitLab Endpoint Agent Platform (%[1]sagent-client%[1]s) through %[1]sglab%[1]s.

			The %[1]ssetup%[1]s, %[1]sdoctor%[1]s, and %[1]saudit%[1]s subcommands manage AI governance. Every other command and flag is forwarded verbatim to the managed %[1]sagent-client%[1]s binary, except the %[1]sglab%[1]s-owned %[1]s--install%[1]s, %[1]s--update%[1]s, and %[1]s--yes%[1]s flags and the %[1]supdate%[1]s command. Run %[1]sglab help minder%[1]s to see them.

			%[1]sglab%[1]s downloads, verifies, and updates the managed binary for you on first use, then runs it. Running the agent-client is Linux-only for now.

			Configuration options:

			- %[1]sminder_cli_auto_run%[1]s: Skip the run confirmation prompt.
			- %[1]sminder_cli_auto_download%[1]s: Skip the download confirmation prompt.
		`, "`") + text.ExperimentalString,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
			"help:environment": heredoc.Docf(`
				- %[1]sGLAB_MINDER_CLI_BINARY_PATH%[1]s: Use a local binary instead of the managed one. Skips
				  download, version checks, and updates. Can also be set via the %[1]sminder_cli_binary_path%[1]s
				  configuration key.
				- %[1]sGLAB_MINDER_CLI_AUTO_DOWNLOAD%[1]s: Set to %[1]strue%[1]s to download the binary without a
				  prompt. Required to run in a non-interactive environment such as CI.
				- %[1]sGLAB_MINDER_CLI_AUTO_RUN%[1]s: Set to %[1]strue%[1]s to run the binary without a prompt.
				  Required to run in a non-interactive environment such as CI.
				`, "`"),
		},
		Example: heredoc.Doc(`
			# Install the GitLab Endpoint Agent Platform into a coding agent
			$ glab minder install
			$ glab minder hook claude-code

			# Show the binary's own help and version
			$ glab minder --help
			$ glab minder version

			# Install or update the managed binary without running it
			$ glab minder --install
			$ glab minder update`),
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(cmd, args)
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	// Registered for `glab help minder` and the docs; DisableFlagParsing routes real parsing through binarymgr.SplitFlags.
	fl := cmd.Flags()
	fl.BoolP("help", "h", false, "Show the agent client's help, or this text until the binary is installed.")
	fl.BoolP("yes", "y", false, "Skip confirmation prompts.")
	fl.Bool("install", false, "Install the agent client binary without running it.")
	fl.Bool("update", false, "Check for and install updates to the binary. Same as the update command.")

	cmd.AddCommand(minderUpdateCmd.NewCmd(f))

	// Attach the governance subcommands (setup, doctor, audit). Cobra routes a
	// matching subcommand before falling through to the pass-through RunE, so
	// these take priority and everything else is forwarded to agent-client.
	governCmd.AddSubcommands(cmd, f)

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) {
	o.flags = binarymgr.SplitFlags(args)
	o.help = cmd.Help
}

func (o *options) validate() error {
	if o.flags.Install && o.flags.Update {
		return errors.New("the --install and --update flags are mutually exclusive")
	}
	if (o.flags.Install || o.flags.Update) && len(o.flags.Forwarded) > 0 {
		return errors.New("the --install and --update flags cannot be combined with a command")
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	if o.flags.HelpOnly() {
		return o.runHelp(ctx)
	}
	runner := binarymgr.NewRunner(o.io, o.cfg, binaries.MinderCLI())
	runner.Yes = o.flags.Yes
	runner.Install = o.flags.Install
	runner.Update = o.flags.Update
	runner.Args = o.flags.Forwarded
	runner.Executor = o.exec
	if runner.Install {
		return runner.HandleInstall(ctx)
	}
	return runner.Run(ctx)
}

func (o *options) runHelp(ctx context.Context) error {
	status, err := binarymgr.InstalledBinary(o.cfg, binaries.MinderCLI())
	if err != nil {
		dbg.Debugf("minder help: %v", err)
	}
	if !status.Installed {
		return o.help()
	}
	return o.exec(ctx, status.Path, o.flags.Forwarded)
}

// exec prepends the literal "agent" subcommand: the downloaded binary's real
// feature tree lives under `agent-client agent ...`, so `glab minder install`
// must invoke `agent-client agent install`.
func (o *options) exec(ctx context.Context, binaryPath string, args []string) error {
	argv := append([]string{"agent"}, args...)
	return o.execute(ctx, o.io, binaryPath, argv)
}
