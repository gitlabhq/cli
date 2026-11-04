// Package autoflow implements the `glab autoflow` command group.
package autoflow

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	workflowCmd "gitlab.com/gitlab-org/cli/internal/commands/autoflow/workflow"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// NewCmd returns the `glab autoflow` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autoflow <command>",
		Short: "Run workflows on GitLab AutoFlow. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Run workflows on the AutoFlow service of GitLab Relay.
		`) + text.ExperimentalString,
		Hidden: true,
	}

	cmd.AddCommand(workflowCmd.NewCmd(f))

	return cmd
}
