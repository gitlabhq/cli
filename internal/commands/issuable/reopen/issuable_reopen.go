package reopen

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var (
	description = map[issuable.IssueType]string{
		issuable.TypeIssue:    "Reopen a closed issue.",
		issuable.TypeIncident: "Reopen a resolved incident.",
	}

	reopeningMessage = map[issuable.IssueType]string{
		issuable.TypeIssue:    "Reopening issue",
		issuable.TypeIncident: "Reopening incident",
	}

	reopenedMessage = map[issuable.IssueType]string{
		issuable.TypeIssue:    "Reopened issue",
		issuable.TypeIncident: "Reopened incident",
	}
)

func NewCmdReopen(f cmdutils.Factory, issueType issuable.IssueType) *cobra.Command {
	examplePath := "issues/123"

	if issueType == issuable.TypeIncident {
		examplePath = "issues/incident/123"
	}

	issueReopenCmd := &cobra.Command{
		Use:   "reopen [<id> | <url>] [flags]",
		Short: description[issueType],
		Long: heredoc.Doc(`
			You can use a full GitLab URL instead of an ID.
		`),
		Aliases: []string{"open"},
		Example: heredoc.Doc(fmt.Sprintf(`
			glab %[1]s reopen 123
			glab %[1]s open 123
			glab %[1]s reopen https://gitlab.com/NAMESPACE/REPO/-/%s`, issueType, examplePath)),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			io := f.IO()
			c := f.IO().Color()

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			issues, repo, err := issueutils.IssuesFromArgs(cmd.Context(), f.ApiClient, client, f.BaseRepo, f.DefaultHostname(), f.Config(), args)
			if err != nil {
				return err
			}

			l := &gitlab.UpdateIssueOptions{}
			l.StateEvent = new("reopen")

			for _, issue := range issues {
				valid, msg := issuable.ValidateIncidentCmd(issueType, "reopen", issue)
				if !valid {
					io.LogInfo(msg)
					continue
				}

				io.LogInfof("- %s...\n", reopeningMessage[issueType])
				issue, err := api.UpdateIssue(client, repo.FullName(), issue.IID, l)
				if err != nil {
					return err
				}

				io.LogInfof("%s %s #%d.\n", c.GreenCheck(), reopenedMessage[issueType], issue.IID)
				io.LogInfo(issueutils.DisplayIssue(c, issue, f.IO().IsaTTY))
			}
			return nil
		},
	}

	return issueReopenCmd
}
