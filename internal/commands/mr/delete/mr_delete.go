package delete

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	mrDeleteCmd := &cobra.Command{
		Use:   "delete [<id> | <branch>]",
		Short: `Delete a merge request.`,
		Long: heredoc.Doc(`
			You can delete multiple merge requests by passing multiple IDs
			or branch names.
		`),
		Aliases: []string{"del"},
		Example: heredoc.Doc(`
			glab mr delete 123

			# Delete multiple merge requests by ID and branch name
			glab mr delete 123 branch-name 789

			# Delete merge requests !1, !2, !3, !4, !5
			glab mr delete 1,2,branch-related-to-mr-3,4,5

			glab mr del 123
			glab mr delete branch`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := f.IO().Color()
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			mrs, repo, err := mrutils.MRsFromArgs(cmd.Context(), f, args, "any")
			if err != nil {
				return err
			}

			for _, mr := range mrs {
				f.IO().LogInfof("- Deleting merge request !%d.\n", mr.IID)
				if err = api.DeleteMR(client, repo.FullName(), mr.IID); err != nil {
					return err
				}
				f.IO().LogInfof("%s Merge request !%d deleted.\n", c.RedCheck(), mr.IID)
			}

			return nil
		},
	}

	return mrDeleteCmd
}
