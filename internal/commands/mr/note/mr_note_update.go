package note

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type updateOptions struct {
	io           *iostreams.IOStreams
	factory      cmdutils.Factory
	gitlabClient func() (*gitlab.Client, error)

	// Flags.
	message string
	attach  []string

	// Populated in complete.
	mrArgs       []string
	client       *gitlab.Client
	mr           *gitlab.MergeRequest
	repo         glrepo.Interface
	discussionID string
	noteID       int64
	body         string
}

func NewCmdUpdate(f cmdutils.Factory) *cobra.Command {
	opts := &updateOptions{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "update [<id> | <branch>] <note-id>",
		Short: "Update the body of a note on a merge request. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Replace the body of an existing note on a merge request.

			%[1]s<note-id>%[1]s is a numeric note ID, not a hex discussion ID.
			You can find note IDs with:

			- %[1]sglab mr note list -F json%[1]s (the %[1]s.id%[1]s field)
			- Note URLs: %[1]s.../merge_requests/1#note_12345%[1]s

			You can change only the note body. You cannot move the position of diff notes.

			%[1]s--attach%[1]s uploads a file and references it at the end of the note. Repeat the flag for more than one file, or pass %[1]s-%[1]s to read the file from standard input. Without %[1]s--message%[1]s the references are added to the body the note already has, instead of replacing it.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Update note 12345 on merge request 1 with a new message
			glab mr note update 1 12345 -m "Updated comment"

			# Update a note on the current branch's merge request, composing in an editor
			glab mr note update 12345

			# Pipe the new body from stdin
			echo "new body" | glab mr note update 1 12345

			# Add a screenshot to the existing note body
			glab mr note update 1 12345 --attach ./screenshot.png
		`),
		Args: cobra.RangeArgs(1, 2),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().StringVarP(&opts.message, "message", "m", "", "New note body. If omitted, opens an editor or reads from stdin.")
	cmdutils.AddAttachFlag(cmd, &opts.attach, "note")

	return cmd
}

func (o *updateOptions) complete(cmd *cobra.Command, args []string) error {
	// Last arg is always the note ID.
	noteIDStr := args[len(args)-1]
	noteID, err := strconv.ParseInt(noteIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid note ID %q: must be a numeric note ID, not a discussion ID prefix", noteIDStr)
	}
	o.noteID = noteID

	if len(args) == 2 {
		o.mrArgs = args[:1]
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	o.client = client

	mr, repo, err := mrutils.MRFromArgs(cmd.Context(), o.factory, o.mrArgs, "any")
	if err != nil {
		return err
	}
	o.mr = mr
	o.repo = repo

	// Find the parent discussion.
	discussions, err := mrutils.ListAllDiscussions(cmd.Context(), client, repo.FullName(), mr.IID, &gitlab.ListMergeRequestDiscussionsOptions{})
	if err != nil {
		return fmt.Errorf("failed to list discussions: %w", err)
	}

	discussionID, note, err := mrutils.FindNoteInDiscussions(discussions, noteID)
	if err != nil {
		return fmt.Errorf("note %d not found in merge request !%d", noteID, mr.IID)
	}
	o.discussionID = discussionID

	// Resolve body.
	body := o.message
	switch {
	case strings.TrimSpace(body) != "":
	case len(o.attach) == 0:
		body, err = getBodyFromStdinOrEditor(o.factory, cmd)
		if err != nil {
			return err
		}
	default:
		body = note.Body
	}
	o.body = body

	return nil
}

func (o *updateOptions) validate() error {
	if strings.TrimSpace(o.body) == "" && len(o.attach) == 0 {
		return fmt.Errorf("aborted: note has an empty message")
	}
	return nil
}

func (o *updateOptions) run(ctx context.Context) error {
	body, err := cmdutils.AppendAttachments(ctx, o.io, o.client, o.repo.FullName(), o.body, o.attach)
	if err != nil {
		return err
	}
	o.body = body

	_, _, err = o.client.Discussions.UpdateMergeRequestDiscussionNote(
		o.repo.FullName(),
		o.mr.IID,
		o.discussionID,
		o.noteID,
		&gitlab.UpdateMergeRequestDiscussionNoteOptions{
			Body: &o.body,
		},
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to update note: %w", err)
	}

	o.io.LogInfof("%s#note_%d\n", o.mr.WebURL, o.noteID)
	return nil
}
