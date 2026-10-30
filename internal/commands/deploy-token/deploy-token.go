package deploytoken

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cmdCreate "gitlab.com/gitlab-org/cli/internal/commands/deploy-token/create"
	cmdGet "gitlab.com/gitlab-org/cli/internal/commands/deploy-token/get"
	cmdList "gitlab.com/gitlab-org/cli/internal/commands/deploy-token/list"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy-token <command>",
		Short: "Manage deploy tokens.",
		Long: heredoc.Docf(`
			Manage the deploy tokens of a project or a group.

			Deploy tokens grant access to a repository, its container registry, and its package registry over HTTPS without being tied to a user account. They are commonly used by CI/CD jobs and external systems.

			These commands act on the current project. Use %[1]s--repo%[1]s to target a different project, or %[1]s--group%[1]s to act on a group instead. If %[1]sGITLAB_GROUP%[1]s is set and neither flag is given, these commands act on that group.
		`, "`"),
	}

	cmdutils.EnableRepoOverride(cmd, f)

	cmd.AddCommand(cmdCreate.NewCmd(f))
	cmd.AddCommand(cmdGet.NewCmd(f))
	cmd.AddCommand(cmdList.NewCmd(f))

	return cmd
}
