package unsubscribe

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var unsubscribeFromMR = func(client *gitlab.Client, projectID any, mrID int64, opts gitlab.RequestOptionFunc) (*gitlab.MergeRequest, error) {
	mr, _, err := client.MergeRequests.UnsubscribeFromMergeRequest(projectID, mrID, opts)
	if err != nil {
		return nil, err
	}

	return mr, nil
}

func NewCmdUnsubscribe(f cmdutils.Factory) *cobra.Command {
	mrUnsubscribeCmd := &cobra.Command{
		Use:   "unsubscribe [<id> | <branch>]",
		Short: `Unsubscribe from a merge request.`,
		Long: heredoc.Doc(`
			You no longer receive notifications after unsubscribing.
		`),
		Aliases: []string{"unsub"},
		Example: heredoc.Doc(`
		# Unsubscribe from a merge request
		glab mr unsubscribe 123
		glab mr unsub 123
		glab mr unsubscribe branch

		# Unsubscribe from multiple merge requests
		glab mr unsubscribe 123 branch`),
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

			mrs, repo, err := mrutils.MRsFromArgs(cmd.Context(), f, args, "any")
			if err != nil {
				return err
			}

			for _, mr := range mrs {
				if err = mrutils.MRCheckErrors(mr, mrutils.MRCheckErrOptions{
					Unsubscribed: true,
				}); err != nil {
					return err
				}

				f.IO().LogInfof("- Unsubscribing from merge request !%d.\n", mr.IID)

				mr, err = unsubscribeFromMR(client, repo.FullName(), mr.IID, nil)
				if err != nil {
					return err
				}

				f.IO().LogInfof("%s You have successfully unsubscribed from merge request !%d.\n", c.RedCheck(), mr.IID)
				f.IO().LogInfo(mrutils.DisplayMR(c, &mr.BasicMergeRequest, f.IO().IsaTTY))
			}

			return nil
		},
	}

	return mrUnsubscribeCmd
}
