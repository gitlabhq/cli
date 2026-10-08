package draft

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	createCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/note/draft/create"
	deleteCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/note/draft/delete"
	listCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/note/draft/list"
	publishCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/note/draft/publish"
	updateCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/note/draft/update"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft <command> [flags]",
		Short: "Manage your pending review comments on a merge request. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Pending review comments, also called draft notes, are visible only to you until you publish your review. Use these commands to add comments to a review, check and edit them, and then publish them all at once.

			Each command acts only on your own pending comments. Other reviewers' pending comments are never listed, changed, or published.
		`) + text.ExperimentalString,
	}

	cmd.AddCommand(createCmd.NewCmd(f))
	cmd.AddCommand(listCmd.NewCmd(f))
	cmd.AddCommand(updateCmd.NewCmd(f))
	cmd.AddCommand(deleteCmd.NewCmd(f))
	cmd.AddCommand(publishCmd.NewCmd(f))
	return cmd
}
