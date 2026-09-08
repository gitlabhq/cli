package issues

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func NewCmdIssues(f cmdutils.Factory) *cobra.Command {
	mrIssuesCmd := &cobra.Command{
		Use:   "issues [<id> | <branch>]",
		Short: `Get issues that close when a merge request is merged.`,
		Long: heredoc.Doc(`
			Lists issues linked to the merge request through closing patterns
			in the description or commits.
		`),
		Aliases: []string{"issue"},
		Args:    cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# List issues for merge request 46
			glab mr issues 46
			glab mr issues branch

			# Use the checked-out branch
			glab mr issues`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			mr, repo, err := mrutils.MRFromArgs(cmd.Context(), f, args, "any")
			if err != nil {
				return err
			}

			l := &gitlab.GetIssuesClosedOnMergeOptions{}

			mrIssues, _, err := client.MergeRequests.GetIssuesClosedOnMerge(repo.FullName(), mr.IID, l)
			if err != nil {
				return err
			}

			title := utils.NewListTitle("issue")
			title.RepoName = repo.FullName()
			title.Page = 0
			title.ListActionType = "search"
			title.CurrentPageTotal = len(mrIssues)

			f.IO().LogInfof("%s\n%s\n", title.Describe(), issueutils.DisplayIssueList(f.IO(), mrIssues, title.RepoName))
			return nil
		},
	}

	return mrIssuesCmd
}
