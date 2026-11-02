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
	var internal bool

	issueNoteCreateCmd := &cobra.Command{
		Use:     fmt.Sprintf("note <%s-id>", issueType),
		Aliases: []string{"comment"},
		Short:   fmt.Sprintf("Comment on an %s in GitLab.", issueType),
		Long: heredoc.Docf(`
			Without %[1]s--message%[1]s, opens an editor at a terminal, and otherwise reads the comment from standard input, such as when input is piped or redirected.

			%[1]s--attach%[1]s uploads a file and references it at the end of the comment. Repeat the flag for more than one file, or pass %[1]s-%[1]s to read the file from standard input. An attachment is content on its own, so a comment with only %[1]s--attach%[1]s neither prompts nor reads a body from standard input.
			%[2]s`, "`", fmt.Sprintf(text.ExperimentalFlagString, "`--attach`")),
		Example: heredoc.Docf(`
			# Comment on %[1]s 123, opening an editor for the message
			glab %[1]s note 123

			# Comment with the message given inline
			glab %[1]s note 123 --message "Looking into this now."

			# Create an internal note, visible only to project members
			glab %[1]s note 123 --message "Rotating the leaked token now." --internal

			# Attach a screenshot alongside the message
			glab %[1]s note 123 --message "Here is the repro." --attach ./screenshot.png

			# Attach an image piped from the clipboard
			pngpaste - | glab %[1]s note 123 --attach -

			# Comment with the message read from a file
			glab %[1]s note 123 < comment.md`, issueType),
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
				body, err = cmdutils.NoteBodyFromStdinOrEditor(cmd.Context(), f.IO(), f.Config, "Message:", "Enter the note's message.")
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

			opts := &gitlab.CreateIssueNoteOptions{Body: &body}
			if internal {
				opts.Internal = &internal
			}

			noteInfo, _, err := client.Notes.CreateIssueNote(repo.FullName(), issue.IID, opts)
			if err != nil {
				return err
			}

			f.IO().LogInfof("%s#note_%d\n", issue.WebURL, noteInfo.ID)
			return nil
		},
	}
	issueNoteCreateCmd.Flags().StringP("message", "m", "", "Message text.")
	issueNoteCreateCmd.Flags().BoolVar(&internal, "internal", false, "Create the note as an internal note, visible only to project members.")
	cmdutils.AddAttachFlag(issueNoteCreateCmd, &attach, "comment")

	return issueNoteCreateCmd
}
