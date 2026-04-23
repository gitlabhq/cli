package duo

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	duoAskCmd "gitlab.com/gitlab-org/cli/internal/commands/duo/ask"
	duoCLICmd "gitlab.com/gitlab-org/cli/internal/commands/duo/cli"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	// Create cli command first
	cliCmd := duoCLICmd.NewCmd(f)

	duoCmd := &cobra.Command{
		Use:   "duo <command> prompt",
		Short: "Work with GitLab Duo.",
		Long: heredoc.Doc(`
			Work with GitLab Duo directly in your terminal. Receive AI-native assistance
			across the software development lifecycle, without switching contexts.

			Retrieve forgotten Git commands and get guidance on Git operations, or interact
			with the GitLab Duo Agent Platform through the GitLab Duo CLI ([Beta](https://docs.gitlab.com/policy/development_stages_support/#beta)).
		`),
		// Default to running cli when no subcommand is provided
		RunE: cliCmd.RunE,
	}

	duoCmd.AddCommand(duoAskCmd.NewCmdAsk(f))
	duoCmd.AddCommand(cliCmd)

	// Allow unknown flags to be passed through to the Duo CLI binary
	duoCmd.FParseErrWhitelist.UnknownFlags = true

	return duoCmd
}
