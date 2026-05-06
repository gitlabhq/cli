package note

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdNote(f cmdutils.Factory, issueType issuable.IssueType) *cobra.Command {
	issueNoteCreateCmd := &cobra.Command{
		Use:     fmt.Sprintf("note <%s-id>", issueType),
		Aliases: []string{"comment"},
		Short:   fmt.Sprintf("Comment on an %s in GitLab.", issueType),
		Long: heredoc.Docf(`
			Opens an editor for the comment if you don't use %[1]s--message%[1]s.
		`, "`"),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			out := f.IO().StdOut

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			issue, repo, err := issueutils.IssueFromArg(f.ApiClient, client, f.BaseRepo, f.DefaultHostname(), args[0])
			if err != nil {
				return err
			}

			valid, msg := issuable.ValidateIncidentCmd(issueType, "comment", issue)
			if !valid {
				fmt.Fprintln(f.IO().StdOut, msg)
				return nil
			}

			body, _ := cmd.Flags().GetString("message")

			if strings.TrimSpace(body) == "" {
				editor, err := cmdutils.GetEditor(f.Config)
				if err != nil {
					return err
				}

				err = f.IO().Editor(cmd.Context(), &body, "Message:", "Enter the note's message.", "", editor)
				if err != nil {
					return err
				}
			}

			if strings.TrimSpace(body) == "" {
				return errors.New("aborted... Note is empty.")
			}

			noteInfo, _, err := client.Notes.CreateIssueNote(repo.FullName(), issue.IID, &gitlab.CreateIssueNoteOptions{Body: &body})
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "%s#note_%d\n", issue.WebURL, noteInfo.ID)
			return nil
		},
	}
	issueNoteCreateCmd.Flags().StringP("message", "m", "", "Message text.")

	return issueNoteCreateCmd
}
