package token

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cmdCreate "gitlab.com/gitlab-org/cli/internal/commands/service-account/token/create"
	cmdList "gitlab.com/gitlab-org/cli/internal/commands/service-account/token/list"
	cmdRevoke "gitlab.com/gitlab-org/cli/internal/commands/service-account/token/revoke"
	cmdRotate "gitlab.com/gitlab-org/cli/internal/commands/service-account/token/rotate"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token <command>",
		Short: "Manage the tokens of a service account.",
		Long: heredoc.Docf(`
			Manage the personal access tokens of a service account of a project or a group.

			- Every token command takes the service account as its first argument, by ID or username. IDs and usernames come from %[1]sglab service-account list%[1]s.
			- A token acts as the service account, limited to the scopes it was created with.

			Tokens of a group service account require GitLab 17.11 or later.
		`, "`"),
	}

	cmdutils.EnableRepoOverride(cmd, f)

	cmd.AddCommand(cmdCreate.NewCmd(f))
	cmd.AddCommand(cmdList.NewCmd(f))
	cmd.AddCommand(cmdRevoke.NewCmd(f))
	cmd.AddCommand(cmdRotate.NewCmd(f))

	return cmd
}
