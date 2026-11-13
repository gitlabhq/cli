package orbit

import (
	"context"
	"errors"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/binarymgr/binaries"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	orbitUpdateCmd "gitlab.com/gitlab-org/cli/internal/commands/orbit/update"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io      *iostreams.IOStreams
	cfg     config.Config
	factory cmdutils.Factory
	execute func(ctx context.Context, io *iostreams.IOStreams, binaryPath string, args, extraEnv []string) error
	flags   glabFlags
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{io: f.IO(), cfg: f.Config(), factory: f, execute: executeOrbit}
	cmd := &cobra.Command{
		Use:   "orbit [<command>] [flags]",
		Short: `Query the GitLab Orbit context graph. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Run the GitLab Orbit CLI through glab.

			Coding agents: run %[1]sglab orbit --yes skills get orbit%[1]s to load the Orbit skill. %[1]s--yes%[1]s installs the binary without a prompt on first use.

			Every command and flag, including %[1]s--help%[1]s, is forwarded verbatim to the managed Orbit binary. glab downloads, verifies, and updates that binary for you on first use. Until the binary is installed, %[1]sglab orbit%[1]s and %[1]s--help%[1]s print install instructions instead. glab passes your resolved GitLab credential to the binary on every invocation, so remote commands such as %[1]sglab orbit query%[1]s need no separate login.

			glab handles only the %[1]supdate%[1]s command and the %[1]s--install%[1]s, %[1]s--update%[1]s, and %[1]s--yes%[1]s flags itself. Run %[1]sglab help orbit%[1]s to see them.

			Prerequisites:

			- Run %[1]sglab auth login%[1]s to authenticate.
			- Orbit must be enabled for your namespace (the %[1]sknowledge_graph%[1]s feature flag).

			Configuration options:

			- %[1]sorbit_cli_auto_run%[1]s: Skip the run confirmation prompt.
			- %[1]sorbit_cli_auto_download%[1]s: Skip the download confirmation prompt.

			For more information, see the [Orbit documentation](https://docs.gitlab.com/orbit/).
		`, "`") + text.ExperimentalString,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
			"help:environment": heredoc.Docf(`
				- %[1]sGLAB_ORBIT_CLI_BINARY_PATH%[1]s: Use a local binary instead of the managed one. Skips
				  download, version checks, and updates. Can also be set via the %[1]sorbit_cli_binary_path%[1]s
				  configuration key.
				- %[1]sORBIT_CLI_AUTO_DOWNLOAD%[1]s: Set to %[1]strue%[1]s to download the binary without a
				  prompt. Required to run in a non-interactive environment such as CI.
				- %[1]sORBIT_CLI_AUTO_RUN%[1]s: Set to %[1]strue%[1]s to run the binary without a prompt.
				  Required to run in a non-interactive environment such as CI.
				`, "`"),
		},
		Example: heredoc.Doc(`
			# Install the managed binary without running it, or update it
			$ glab orbit --install --yes
			$ glab orbit update

			# Install on first use without a prompt, then load the Orbit skill
			$ glab orbit --yes skills get orbit

			# Show the Orbit binary's own help
			$ glab orbit --help`),
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	// Registered for `glab help orbit` and the docs; DisableFlagParsing routes real parsing through splitGlabFlags.
	fl := cmd.Flags()
	fl.BoolP("help", "h", false, "Show the Orbit binary's help, or install instructions until the binary is installed.")
	fl.BoolP("yes", "y", false, "Skip confirmation prompts.")
	fl.Bool("install", false, "Install the Orbit binary without running it.")
	fl.Bool("update", false, "Check for and install updates to the binary. Same as the update command.")

	cmd.AddCommand(orbitUpdateCmd.NewCmd(f))

	return cmd
}

func (o *options) complete(args []string) {
	o.flags = splitGlabFlags(args)
}

func (o *options) validate() error {
	if o.flags.install && o.flags.update {
		return errors.New("the --install and --update flags are mutually exclusive")
	}
	if (o.flags.install || o.flags.update) && len(o.flags.forwarded) > 0 {
		return errors.New("the --install and --update flags cannot be combined with a command")
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	if o.flags.helpOnly() {
		return o.runHelp(ctx)
	}
	runner := newRunner(o.io, o.cfg, binaries.Orbit())
	runner.Yes = o.flags.yes
	runner.Install = o.flags.install
	runner.Update = o.flags.update
	runner.Args = o.flags.forwarded
	runner.Executor = o.exec
	if runner.Install {
		return runner.HandleInstall(ctx)
	}
	return runner.Run(ctx)
}

func (o *options) runHelp(ctx context.Context) error {
	spec := binaries.Orbit()
	status, err := binarymgr.InstalledBinary(o.cfg, spec)
	if err != nil {
		dbg.Debugf("orbit help: %v", err)
	}
	if status.Installed {
		return o.exec(ctx, status.Path, o.flags.forwarded)
	}
	managedPath, _ := binarymgr.ManagedBinaryPath(spec)
	if status.Path != "" && status.Path != managedPath {
		o.io.LogInfof("The configured %s path %q was not found or is not executable. Fix or unset %s or the %s configuration key.\n", spec.DisplayName, status.Path, "GLAB_ORBIT_CLI_BINARY_PATH", "orbit_cli_binary_path")
		return nil
	}
	o.io.LogInfof(heredoc.Doc(`
		The %[1]s is not installed.

		Install it with one of:
		  glab orbit --install --yes    Install the binary without running it.
		  glab orbit --yes <command>    Install it on first use, then run the command.

		Run 'glab help orbit' for the flags glab handles itself.
	`), spec.DisplayName)
	return nil
}

func (o *options) exec(ctx context.Context, binaryPath string, args []string) error {
	return o.execute(ctx, o.io, binaryPath, args, orbitCredentialEnv(ctx, o.factory))
}

type glabFlags struct {
	yes       bool
	install   bool
	update    bool
	forwarded []string
}

func (g glabFlags) helpOnly() bool {
	if g.install || g.update {
		return false
	}
	return len(g.forwarded) == 0 || g.forwarded[0] == "--help" || g.forwarded[0] == "-h"
}

func splitGlabFlags(args []string) glabFlags {
	var flags glabFlags
	for i := range len(args) {
		switch arg := args[i]; {
		case arg == "--yes" || arg == "-y":
			flags.yes = true
		case arg == "--install":
			flags.install = true
		case arg == "--update":
			flags.update = true
		case arg == "--":
			flags.forwarded = args[i+1:]
			return flags
		default:
			flags.forwarded = args[i:]
			return flags
		}
	}
	return flags
}

func newRunner(io *iostreams.IOStreams, cfg config.Config, spec binarymgr.Spec) *binarymgr.Runner {
	return &binarymgr.Runner{
		IO:      io,
		Cfg:     cfg,
		Spec:    spec,
		Manager: binarymgr.NewManager(io, spec),
	}
}
