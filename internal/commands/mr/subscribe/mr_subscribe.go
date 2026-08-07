package subscribe

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var subscribeToMR = func(client *gitlab.Client, projectID any, mrID int64, opts gitlab.RequestOptionFunc) (*gitlab.MergeRequest, error) {
	mr, _, err := client.MergeRequests.SubscribeToMergeRequest(projectID, mrID, opts)
	if err != nil {
		return nil, err
	}

	return mr, nil
}

func NewCmdSubscribe(f cmdutils.Factory) *cobra.Command {
	mrSubscribeCmd := &cobra.Command{
		Use:   "subscribe [<id> | <branch>]",
		Short: `Subscribe to a merge request.`,
		Long: heredoc.Doc(`
			You receive notifications for updates when subscribed.
		`),
		Aliases: []string{"sub"},
		Example: heredoc.Doc(`
		# Subscribe to a merge request
		glab mr subscribe 123
		glab mr sub 123
		glab mr subscribe branch

		# Subscribe to multiple merge requests
		glab mr subscribe 123 branch`),
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
					Subscribed: true,
				}); err != nil {
					return err
				}

				f.IO().LogInfof("- Subscribing to merge request !%d.\n", mr.IID)

				mr, err = subscribeToMR(client, repo.FullName(), mr.IID, nil)
				if err != nil {
					return err
				}

				f.IO().LogInfof("%s You have successfully subscribed to merge request !%d.\n", c.GreenCheck(), mr.IID)
				f.IO().LogInfo(mrutils.DisplayMR(c, &mr.BasicMergeRequest, f.IO().IsaTTY))
			}

			return nil
		},
	}

	return mrSubscribeCmd
}
