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

type deleteOptions struct {
	io           *iostreams.IOStreams
	factory      cmdutils.Factory
	gitlabClient func() (*gitlab.Client, error)

	// Flags.
	yes bool

	// Populated in complete.
	mrArgs       []string
	client       *gitlab.Client
	mr           *gitlab.MergeRequest
	repo         glrepo.Interface
	discussionID string
	noteID       int64
	note         *gitlab.Note
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &deleteOptions{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "delete [<id> | <branch>] <note-id>",
		Short: "Delete a note from a merge request. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Permanently delete a note from a merge request.

			%[1]s<note-id>%[1]s is a numeric note ID, not a hex discussion ID.
			You can find note IDs with:

			- %[1]sglab mr note list -F json%[1]s (the %[1]s.id%[1]s field)
			- Note URLs: %[1]s.../merge_requests/1#note_12345%[1]s

			Deletion is permanent and cannot be undone. Unless you pass %[1]s--yes%[1]s,
			the command prompts you to confirm.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Delete note 12345 from merge request 1
			glab mr note delete 1 12345

			# Delete without confirmation
			glab mr note delete 1 12345 --yes

			# Delete a note on the current branch's merge request
			glab mr note delete 12345
		`),
		Args: cobra.RangeArgs(1, 2),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd.Context(), args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt.")

	return cmd
}

func (o *deleteOptions) complete(ctx context.Context, args []string) error {
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

	mr, repo, err := mrutils.MRFromArgs(ctx, o.factory, o.mrArgs, "any")
	if err != nil {
		return err
	}
	o.mr = mr
	o.repo = repo

	// Find the parent discussion and note.
	discussions, err := mrutils.ListAllDiscussions(ctx, client, repo.FullName(), mr.IID, &gitlab.ListMergeRequestDiscussionsOptions{})
	if err != nil {
		return fmt.Errorf("failed to list discussions: %w", err)
	}

	o.discussionID, o.note, err = mrutils.FindNoteInDiscussions(discussions, noteID)
	if err != nil {
		return fmt.Errorf("note %d not found in merge request !%d", noteID, mr.IID)
	}

	return nil
}

func (o *deleteOptions) run(ctx context.Context) error {
	if !o.yes && !o.io.PromptEnabled() {
		return cmdutils.FlagError{Err: fmt.Errorf("--yes required when not running interactively")}
	}

	if !o.yes && o.io.PromptEnabled() {
		body := o.note.Body
		if r := []rune(body); len(r) > 80 {
			body = string(r[:80]) + "..."
		}
		body = strings.ReplaceAll(body, "\n", " ")

		author := ""
		if o.note.Author.Username != "" {
			author = fmt.Sprintf(" by @%s", o.note.Author.Username)
		}

		o.io.LogInfof("Note %d%s: %s\n", o.noteID, author, body)

		var confirmed bool
		if err := o.io.Confirm(ctx, &confirmed, "Are you sure you want to delete this note?"); err != nil {
			return err
		}
		if !confirmed {
			o.io.LogInfo("Aborted.")
			return nil
		}
	}

	_, err := o.client.Discussions.DeleteMergeRequestDiscussionNote(
		o.repo.FullName(),
		o.mr.IID,
		o.discussionID,
		o.noteID,
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to delete note: %w", err)
	}

	o.io.LogInfof("✓ Deleted note %d from !%d\n", o.noteID, o.mr.IID)
	return nil
}
