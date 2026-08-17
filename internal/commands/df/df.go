package df

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cmdCISummary "gitlab.com/gitlab-org/cli/internal/commands/df/cisummary"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dependency-firewall <command>",
		Aliases: []string{"df"},
		Short:   "Configure and monitor GitLab Dependency Firewall for local package managers. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Commands to configure GitLab Dependency Firewall for local package
			managers, run local package managers with a summary of blocked or
			flagged packages, and view activity during the current session.
		`) + text.ExperimentalString,
	}

	cmd.AddCommand(cmdCISummary.NewCmd(f))

	return cmd
}
