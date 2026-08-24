package orbit

import (
	"context"
	"errors"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orbit [<command>] [flags]",
		Short: `GitLab Knowledge Graph commands. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Run the Orbit CLI for the GitLab Knowledge Graph (product name: Orbit).

			Every command and flag is forwarded verbatim to the managed Orbit binary, which is downloaded, verified, and kept up to date for you on first use. %[1]sglab orbit remote <command>%[1]s authenticates automatically using your resolved GitLab credential; all other commands run the binary with no extra environment.

			Prerequisites:

			- Run %[1]sglab auth login%[1]s to authenticate.
			- Orbit must be enabled for your namespace (the %[1]sknowledge_graph%[1]s feature flag).

			Configuration options:

			- %[1]sorbit_local_auto_run%[1]s: Skip the run confirmation prompt.
			- %[1]sorbit_local_auto_download%[1]s: Skip the download confirmation prompt.

			For more information, see the [Orbit documentation](https://docs.gitlab.com/orbit/).
		`, "`") + text.ExperimentalString,
		Annotations: map[string]string{
			"help:environment": heredoc.Docf(`
				- %[1]sGLAB_ORBIT_LOCAL_BINARY_PATH%[1]s: Use a local binary instead of the managed one. Skips
				  download, version checks, and updates. Can also be set via the %[1]sorbit_local_binary_path%[1]s
				  configuration key.
				- %[1]sORBIT_LOCAL_AUTO_DOWNLOAD%[1]s: Set to %[1]strue%[1]s to download the binary without a
				  prompt. Required to run in a non-interactive environment such as CI.
				- %[1]sORBIT_LOCAL_AUTO_RUN%[1]s: Set to %[1]strue%[1]s to run the binary without a prompt.
				  Required to run in a non-interactive environment such as CI.
				`, "`"),
		},
		Example: heredoc.Doc(`
			# Guided onboarding (choose your assistant)
			$ glab orbit setup claude

			# Discover and query the remote Knowledge Graph (authenticates automatically)
			$ glab orbit remote status
			$ glab orbit remote query ./query.json
			$ glab orbit remote graph-status --full-path gitlab-org/gitlab

			# Index and query a local copy of the graph
			$ glab orbit local index
			$ glab orbit local sql "SELECT 1"

			# Show the Orbit binary version
			$ glab orbit version

			# Install or update the managed binary without running it
			$ glab orbit --install
			$ glab orbit --update`),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runner, showHelp, err := newPassthroughRunner(f, args)
			if err != nil {
				return err
			}
			if showHelp {
				return cmd.Help()
			}
			if runner.Install {
				return runner.HandleInstall(cmd.Context())
			}
			return runner.Run(cmd.Context())
		},
	}

	// Registered for --help only; DisableFlagParsing routes real parsing through splitGlabFlags.
	fl := cmd.Flags()
	fl.BoolP("yes", "y", false, "Skip confirmation prompts.")
	fl.Bool("install", false, "Install the Orbit binary without running it.")
	fl.Bool("update", false, "Check for and install updates to the binary.")

	return cmd
}

func newPassthroughRunner(f cmdutils.Factory, args []string) (*binarymgr.Runner, bool, error) {
	flags := splitGlabFlags(args)
	if flags.showHelp {
		return nil, true, nil
	}
	if flags.install && flags.update {
		return nil, false, errors.New("the --install and --update flags are mutually exclusive")
	}
	if (flags.install || flags.update) && len(flags.forwarded) > 0 {
		return nil, false, errors.New("the --install and --update flags cannot be combined with a command")
	}

	io := f.IO()
	runner := newRunner(f.IO(), f.Config(), Spec())
	runner.Yes = flags.yes
	runner.Install = flags.install
	runner.Update = flags.update
	runner.Args = flags.forwarded
	runner.Executor = func(ctx context.Context, binaryPath string, execArgs []string) error {
		return executeOrbit(ctx, io, binaryPath, execArgs, orbitCredentialEnv(ctx, f))
	}

	return runner, false, nil
}

type glabFlags struct {
	yes       bool
	install   bool
	update    bool
	showHelp  bool
	forwarded []string
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
		case arg == "--help" || arg == "-h":
			flags.showHelp = true
			return flags
		case arg == "--":
			flags.forwarded = args[i+1:]
			return flags
		default:
			flags.forwarded = args[i:]
			return flags
		}
	}

	if len(flags.forwarded) == 0 && !flags.install && !flags.update {
		flags.showHelp = true
	}
	return flags
}
