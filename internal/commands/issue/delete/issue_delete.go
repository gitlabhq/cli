package delete

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var deleteIssue = func(client *gitlab.Client, projectID any, issueID int64) error {
	_, err := client.Issues.DeleteIssue(projectID, issueID)
	if err != nil {
		return err
	}

	return nil
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	issueDeleteCmd := &cobra.Command{
		Use:   "delete <id>",
		Short: `Delete an issue.`,
		Long: heredoc.Doc(`
			Permanently deletes the issue. You can pass an issue ID or a full
			issue URL.
		`),
		Aliases: []string{"del"},
		Example: heredoc.Doc(`
			glab issue delete 123
			glab issue del 123
			glab issue delete https://gitlab.com/profclems/glab/-/issues/123`),
		Args: cobra.ExactArgs(1),
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

			issues, repo, err := issueutils.IssuesFromArgs(cmd.Context(), f.ApiClient, client, f.BaseRepo, f.DefaultHostname(), f.Config(), args)
			if err != nil {
				return err
			}

			for _, issue := range issues {
				if f.IO().IsErrTTY && f.IO().IsaTTY {
					f.IO().LogErrorf("- Deleting issue #%d.\n", issue.IID)
				}

				err := deleteIssue(client, repo.FullName(), issue.IID)
				if err != nil {
					return err
				}

				f.IO().LogError(c.GreenCheck(), "Issue deleted.")
			}
			return nil
		},
	}
	return issueDeleteCmd
}
