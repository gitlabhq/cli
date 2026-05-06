package subscribe

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

// errIssuableUserAlreadySubscribed received when trying to subscribe to an issue the user is already subscribed to
var errIssuableUserAlreadySubscribed = errors.New("you are already subscribed to this issue")

var subscribingMessage = map[issuable.IssueType]string{
	issuable.TypeIssue:    "Subscribing to issue",
	issuable.TypeIncident: "Subscribing to incident",
}

func NewCmdSubscribe(f cmdutils.Factory, issueType issuable.IssueType) *cobra.Command {
	examplePath := "issues/123"

	if issueType == issuable.TypeIncident {
		examplePath = "issues/incident/123"
	}

	issueSubscribeCmd := &cobra.Command{
		Use:   "subscribe <id>",
		Short: fmt.Sprintf(`Subscribe to an %s.`, issueType),
		Long: heredoc.Docf(`
			You receive notifications when the %s is updated, commented on,
			or has its state changed.
		`, issueType),
		Aliases: []string{"sub"},
		Example: heredoc.Doc(fmt.Sprintf(`
			glab %[1]s subscribe 123
			glab %[1]s sub 123
			glab %[1]s subscribe https://gitlab.com/OWNER/REPO/-/%[2]s`, issueType, examplePath)),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c := f.IO().Color()
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			issues, repo, err := issueutils.IssuesFromArgs(cmd.Context(), f.ApiClient, client, f.BaseRepo, f.DefaultHostname(), args)
			if err != nil {
				return err
			}

			for _, issue := range issues {
				valid, msg := issuable.ValidateIncidentCmd(issueType, "subscribe", issue)
				if !valid {
					fmt.Fprintln(f.IO().StdOut, msg)
					continue
				}

				if f.IO().IsaTTY && f.IO().IsErrTTY {
					fmt.Fprintf(
						f.IO().StdOut,
						"- %s #%d in %s\n",
						subscribingMessage[issueType],
						issue.IID,
						c.Cyan(repo.FullName()),
					)
				}

				issue, err := subscribe(client, repo.FullName(), int(issue.IID))
				if err != nil {
					if errors.Is(err, errIssuableUserAlreadySubscribed) {
						fmt.Fprintf(
							f.IO().StdOut,
							"%s You are already subscribed to this %s.\n\n",
							c.FailedIcon(),
							issueType,
						)
						return nil // the error already handled
					}
					return err
				}

				fmt.Fprintln(f.IO().StdOut, c.GreenCheck(), "Subscribed")
				fmt.Fprintln(f.IO().StdOut, issueutils.DisplayIssue(c, issue, f.IO().IsaTTY))
			}
			return nil
		},
	}

	return issueSubscribeCmd
}

func subscribe(client *gitlab.Client, projectID any, issueID int) (*gitlab.Issue, error) {
	issue, resp, err := client.Issues.SubscribeToIssue(projectID, int64(issueID))
	if err != nil {
		if resp != nil {
			// If the user is already subscribed to the issue, the status code 304 is returned.
			if resp.StatusCode == http.StatusNotModified {
				return nil, errIssuableUserAlreadySubscribed
			}
		}
		return issue, err
	}

	return issue, nil
}
