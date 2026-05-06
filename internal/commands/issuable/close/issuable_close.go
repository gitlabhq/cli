package close

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var closingMessage = map[issuable.IssueType]string{
	issuable.TypeIssue:    "Closing issue",
	issuable.TypeIncident: "Resolving incident",
}

var closedMessage = map[issuable.IssueType]string{
	issuable.TypeIssue:    "Closed issue",
	issuable.TypeIncident: "Resolved incident",
}

func NewCmdClose(f cmdutils.Factory, issueType issuable.IssueType) *cobra.Command {
	examplePath := "issues/123"
	aliases := []string{}

	if issueType == issuable.TypeIncident {
		examplePath = "issues/incident/123"
		aliases = []string{"resolve"}
	}

	issueCloseCmd := &cobra.Command{
		Use:   "close [<id> | <url>] [flags]",
		Short: fmt.Sprintf(`Close an %s.`, issueType),
		Long: heredoc.Doc(`
			You can use a full GitLab URL instead of an ID.
		`),
		Aliases: aliases,
		Example: heredoc.Doc(fmt.Sprintf(`
			glab %[1]s close 123
			glab %[1]s close https://gitlab.com/NAMESPACE/REPO/-/%s`, issueType, examplePath)),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			issues, repo, err := issueutils.IssuesFromArgs(cmd.Context(), f.ApiClient, client, f.BaseRepo, f.DefaultHostname(), args)
			if err != nil {
				return err
			}

			l := &gitlab.UpdateIssueOptions{}
			l.StateEvent = new("close")

			c := f.IO().Color()

			for _, issue := range issues {
				valid, msg := issuable.ValidateIncidentCmd(issueType, "close", issue)
				if !valid {
					fmt.Fprintln(f.IO().StdOut, msg)
					continue
				}

				fmt.Fprintf(f.IO().StdOut, "- %s...\n", closingMessage[issueType])
				issue, err := api.UpdateIssue(client, repo.FullName(), issue.IID, l)
				if err != nil {
					return err
				}
				fmt.Fprintf(f.IO().StdOut, "%s %s #%d\n", c.RedCheck(), closedMessage[issueType], issue.IID)
				fmt.Fprintln(f.IO().StdOut, issueutils.DisplayIssue(c, issue, f.IO().IsaTTY))
			}
			return nil
		},
	}
	return issueCloseCmd
}
