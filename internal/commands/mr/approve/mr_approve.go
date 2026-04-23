package approve

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdApprove(f cmdutils.Factory) *cobra.Command {
	mrApproveCmd := &cobra.Command{
		Use:   "approve [<id | branch>]",
		Short: `Approve merge requests.`,
		Long:  ``,
		Example: heredoc.Doc(`
			glab mr approve 235
			glab mr approve 123 345
			glab mr approve branch-1
			glab mr approve branch-2 branch-3

			# Finds open merge request from current branch and approves it
			glab mr approve`),
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

				opts := &gitlab.ApproveMergeRequestOptions{}
				if s, _ := cmd.Flags().GetString("sha"); s != "" {
					opts.SHA = new(s)
				}

				fmt.Fprintf(f.IO().StdOut, "- Approving merge request !%d\n", mr.IID)
				_, _, err := client.MergeRequestApprovals.ApproveMergeRequest(repo.FullName(), mr.IID, opts)
				if err != nil {
					return err
				}
				fmt.Fprintln(f.IO().StdOut, c.GreenCheck(), "Approved")
			}

			return nil
		},
	}

	// mrApproveCmd.Flags().StringP("password", "p", "", "Current user’s password. Required if 'Require user password to approve' is enabled in the project settings.")
	mrApproveCmd.Flags().StringP("sha", "s", "", "SHA, which must match the SHA of the HEAD commit of the merge request.")
	return mrApproveCmd
}
