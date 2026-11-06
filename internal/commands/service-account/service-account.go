package serviceaccount

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cmdGet "gitlab.com/gitlab-org/cli/internal/commands/service-account/get"
	cmdList "gitlab.com/gitlab-org/cli/internal/commands/service-account/list"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service-account <command>",
		Short: "Manage service accounts.",
		Long: heredoc.Docf(`
			Manage the service accounts of a project or a group.

			Service accounts are user accounts that represent a non-human entity, such as a CI/CD job, a script, or an external system, rather than a person.

			These commands act on the current project. Use %[1]s--repo%[1]s to target a different project, or %[1]s--group%[1]s to act on a group instead. If %[1]sGITLAB_GROUP%[1]s is set and neither flag is given, these commands act on that group.

			Group service accounts require the Owner role for the group. On GitLab Self-Managed and GitLab Dedicated, they require an administrator, or the Owner role when an administrator has turned on the setting to allow top-level group Owners to create service accounts.

			Project service accounts require the Maintainer or Owner role for the project, or an administrator. They require GitLab 18.11 or later.
		`, "`"),
	}

	cmdutils.EnableRepoOverride(cmd, f)

	cmd.AddCommand(cmdGet.NewCmd(f))
	cmd.AddCommand(cmdList.NewCmd(f))

	return cmd
}
