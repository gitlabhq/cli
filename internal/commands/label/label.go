package label

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	labelCreateCmd "gitlab.com/gitlab-org/cli/internal/commands/label/create"
	labelDeleteCmd "gitlab.com/gitlab-org/cli/internal/commands/label/delete"
	labelUpdateCmd "gitlab.com/gitlab-org/cli/internal/commands/label/edit"
	labelGetCmd "gitlab.com/gitlab-org/cli/internal/commands/label/get"
	labelListCmd "gitlab.com/gitlab-org/cli/internal/commands/label/list"
)

func NewCmdLabel(f cmdutils.Factory) *cobra.Command {
	labelCmd := &cobra.Command{
		Use:   "label <command> [flags]",
		Short: `Manage project and group labels.`,
		Long: heredoc.Doc(`
		Manage labels on projects and groups. Labels are used to categorize
		and filter issues, merge requests, and epics. Use the subcommands to
		create, list, edit, delete, or look up labels in the current repository
		or in another project or group.
		`),
	}

	cmdutils.EnableRepoOverride(labelCmd, f)

	labelCmd.AddCommand(labelListCmd.NewCmdList(f))
	labelCmd.AddCommand(labelCreateCmd.NewCmdCreate(f))
	labelCmd.AddCommand(labelDeleteCmd.NewCmdDelete(f))
	labelCmd.AddCommand(labelUpdateCmd.NewCmdEdit(f))
	labelCmd.AddCommand(labelGetCmd.NewCmdGet(f))

	return labelCmd
}
