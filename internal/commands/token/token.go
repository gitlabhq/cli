package token

import (
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/token/create"
	"gitlab.com/gitlab-org/cli/internal/commands/token/list"
	"gitlab.com/gitlab-org/cli/internal/commands/token/revoke"
	"gitlab.com/gitlab-org/cli/internal/commands/token/rotate"
)

func NewTokenCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "token",
		Short:   "Manage personal, project, or group tokens.",
		Aliases: []string{"token"},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.AddCommand(create.NewCmdCreate(f))
	cmd.AddCommand(revoke.NewCmdRevoke(f))
	cmd.AddCommand(rotate.NewCmdRotate(f))
	cmd.AddCommand(list.NewCmdList(f))
	return cmd
}
