package note

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmdNote(f cmdutils.Factory, issueType issuable.IssueType) *cobra.Command {
	var attach []string

	issueNoteCreateCmd := &cobra.Command{
		Use:     fmt.Sprintf("note <%s-id>", issueType),
		Aliases: []string{"comment"},
		Short:   fmt.Sprintf("Comment on an %s in GitLab.", issueType),
		Long: heredoc.Docf(`
			Opens an editor for the comment if you don't use %[1]s--message%[1]s.

			%[1]s--attach%[1]s uploads a file and references it at the end of the comment. Repeat the flag for more than one file, or pass %[1]s-%[1]s to read the file from standard input. An attachment is content on its own, so a comment with only %[1]s--attach%[1]s skips the editor.
			%[2]s`, "`", fmt.Sprintf(text.ExperimentalFlagString, "`--attach`")),
		Example: heredoc.Docf(`
			# Comment on %[1]s 123, opening an editor for the message
			glab %[1]s note 123

			# Comment with the message given inline
			glab %[1]s note 123 --message "Looking into this now."

			# Attach a screenshot alongside the message
			glab %[1]s note 123 --message "Here is the repro." --attach ./screenshot.png

			# Attach an image piped from the clipboard
			pngpaste - | glab %[1]s note 123 --attach -`, issueType),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			issue, repo, err := issueutils.IssueFromArg(f.ApiClient, client, f.BaseRepo, f.DefaultHostname(), f.Config(), args[0])
			if err != nil {
				return err
			}

			valid, msg := issuable.ValidateIncidentCmd(issueType, "comment", issue)
			if !valid {
				f.IO().LogInfo(msg)
				return nil
			}

			body, _ := cmd.Flags().GetString("message")

			if strings.TrimSpace(body) == "" && len(attach) == 0 {
				editor, err := cmdutils.GetEditor(f.Config)
				if err != nil {
					return err
				}

				err = f.IO().Editor(cmd.Context(), &body, "Message:", "Enter the note's message.", "", editor)
				if err != nil {
					return err
				}
			}

			body, err = cmdutils.AppendAttachments(cmd.Context(), f.IO(), client, repo.FullName(), body, attach)
			if err != nil {
				return err
			}

			if strings.TrimSpace(body) == "" {
				return errors.New("aborted: note is empty")
			}

			noteInfo, _, err := client.Notes.CreateIssueNote(repo.FullName(), issue.IID, &gitlab.CreateIssueNoteOptions{Body: &body})
			if err != nil {
				return err
			}

			f.IO().LogInfof("%s#note_%d\n", issue.WebURL, noteInfo.ID)
			return nil
		},
	}
	issueNoteCreateCmd.Flags().StringP("message", "m", "", "Message text.")
	cmdutils.AddAttachFlag(issueNoteCreateCmd, &attach, "comment")

	return issueNoteCreateCmd
}
