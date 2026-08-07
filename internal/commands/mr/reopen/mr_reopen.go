package reopen

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdReopen(f cmdutils.Factory) *cobra.Command {
	mrReopenCmd := &cobra.Command{
		Use:   "reopen [<id>... | <branch>...]",
		Short: `Reopen a merge request.`,
		Long: heredoc.Doc(`
			You can reopen multiple merge requests by passing multiple IDs
			or branch names.
		`),
		Example: heredoc.Doc(`
		# Reopen merge request 123
		glab mr reopen 123

		# Reopen merge requests 123, 456, and 789
		glab mr reopen 123 456 789

		# Reopen merge requests from branches branch-1 and branch-2
		glab mr reopen branch-1 branch-2`),
		Aliases: []string{"open"},
		Args:    cobra.ArbitraryArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c := f.IO().Color()
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			mrs, repo, err := mrutils.MRsFromArgs(cmd.Context(), f, args, "closed")
			if err != nil {
				return err
			}

			l := &gitlab.UpdateMergeRequestOptions{}
			l.StateEvent = new("reopen")
			for _, mr := range mrs {
				if err = mrutils.MRCheckErrors(mr, mrutils.MRCheckErrOptions{
					Opened: true,
					Merged: true,
				}); err != nil {
					return err
				}

				f.IO().LogInfof("- Reopening merge request !%d...\n", mr.IID)
				mr, err = api.UpdateMR(client, repo.FullName(), mr.IID, l)
				if err != nil {
					return err
				}

				f.IO().LogInfof("%s Reopened merge request !%d.\n", c.GreenCheck(), mr.IID)
				f.IO().LogInfo(mrutils.DisplayMR(f.IO().Color(), &mr.BasicMergeRequest, f.IO().IsaTTY))
			}

			return nil
		},
	}

	return mrReopenCmd
}
