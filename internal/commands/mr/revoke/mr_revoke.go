package revoke

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdRevoke(f cmdutils.Factory) *cobra.Command {
	mrRevokeCmd := &cobra.Command{
		Use:   "revoke [<id> | <branch>]",
		Short: `Revoke approval on a merge request.`,
		Long: heredoc.Doc(`
			Defaults to the currently checked-out branch.
		`),
		Aliases: []string{"unapprove"},
		Example: heredoc.Doc(`
		# Revoke approval on a merge request
		glab mr revoke 123
		glab mr unapprove 123
		glab mr revoke branch

		# Revoke approval on the currently checked out branch
		glab mr revoke
		# Revoke approval on merge request 123 on branch 456
		glab mr revoke 123 branch 456`),
		Args: cobra.ArbitraryArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			c := f.IO().Color()

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			mrs, repo, err := mrutils.MRsFromArgs(cmd.Context(), f, args, "opened")
			if err != nil {
				return err
			}

			for _, mr := range mrs {
				if err = mrutils.MRCheckErrors(mr, mrutils.MRCheckErrOptions{
					Closed: true,
					Merged: true,
				}); err != nil {
					return err
				}

				f.IO().LogInfof("- Revoking approval for merge request !%d...\n", mr.IID)

				_, err := client.MergeRequestApprovals.UnapproveMergeRequest(repo.FullName(), mr.IID)
				if err != nil {
					return err
				}

				f.IO().LogInfof("%s %s\n", c.GreenCheck(), "Merge request approval revoked.")
			}

			return nil
		},
	}

	return mrRevokeCmd
}
