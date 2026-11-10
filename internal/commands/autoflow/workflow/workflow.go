// Package workflow implements the `glab autoflow workflow` command group.
package workflow

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cancelCmd "gitlab.com/gitlab-org/cli/internal/commands/autoflow/workflow/cancel"
	getCmd "gitlab.com/gitlab-org/cli/internal/commands/autoflow/workflow/get"
	sendCmd "gitlab.com/gitlab-org/cli/internal/commands/autoflow/workflow/send"
	startCmd "gitlab.com/gitlab-org/cli/internal/commands/autoflow/workflow/start"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// NewCmd returns the `glab autoflow workflow` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow <command>",
		Short: "Manage AutoFlow workflows. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Start, read, cancel and send values to workflows on the AutoFlow service of GitLab Relay.
		`) + text.ExperimentalString,
	}

	cmd.AddCommand(startCmd.NewCmd(f))
	cmd.AddCommand(getCmd.NewCmd(f))
	cmd.AddCommand(cancelCmd.NewCmd(f))
	cmd.AddCommand(sendCmd.NewCmd(f))

	return cmd
}
