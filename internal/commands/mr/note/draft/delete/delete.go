package delete

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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

type options struct {
	io           *iostreams.IOStreams
	factory      cmdutils.Factory
	gitlabClient func() (*gitlab.Client, error)

	// Flags.
	yes bool

	// Populated in complete.
	client      *gitlab.Client
	mr          *gitlab.MergeRequest
	repo        glrepo.Interface
	draftNoteID int64
	draftBody   string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "delete [<id> | <branch>] <draft-note-id>",
		Short: "Delete one of your pending review comments from a merge request. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Discard a pending review comment before you publish the review. %[1]s<draft-note-id>%[1]s is the ID printed by %[1]sglab mr note draft create%[1]s or listed by %[1]sglab mr note draft list%[1]s.

			Deletion is permanent and cannot be undone. Unless you pass %[1]s--yes%[1]s, the command shows the comment and prompts you to confirm. When not running interactively, %[1]s--yes%[1]s is required.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Delete pending review comment 456 from merge request 123
			glab mr note draft delete 123 456

			# Delete a pending review comment on the current branch's merge request
			glab mr note draft delete 456

			# Delete without confirmation
			glab mr note draft delete 123 456 --yes
		`),
		Args: cobra.RangeArgs(1, 2),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if err := opts.complete(cmd.Context(), args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt.")

	return cmd
}

func (o *options) complete(ctx context.Context, args []string) error {
	idArg := args[len(args)-1]
	draftNoteID, err := strconv.ParseInt(idArg, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid pending review comment ID %q: must be a number", idArg)
	}
	o.draftNoteID = draftNoteID

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	o.client = client

	mr, repo, err := mrutils.MRFromArgs(ctx, o.factory, args[:len(args)-1], "any")
	if err != nil {
		return err
	}
	o.mr = mr
	o.repo = repo

	draft, err := mrutils.GetDraftNote(ctx, client, repo.FullName(), mr.IID, draftNoteID)
	if err != nil {
		return err
	}
	o.draftBody = draft.Note
	return nil
}

// validate checks only flag values, so it runs before complete to reject bad
// input without any API calls.
func (o *options) validate() error {
	if !o.yes && !o.io.PromptEnabled() {
		return cmdutils.FlagError{Err: errors.New("--yes required when not running interactively")}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	if !o.yes {
		o.io.LogInfof("Pending review comment %d: %s\n", o.draftNoteID, mrutils.NotePreview(o.draftBody))

		var confirmed bool
		if err := o.io.Confirm(ctx, &confirmed, "Delete this pending review comment?"); err != nil {
			return err
		}
		if !confirmed {
			o.io.LogInfo("Aborted.")
			return nil
		}
	}

	if _, err := o.client.DraftNotes.DeleteDraftNote(o.repo.FullName(), o.mr.IID, o.draftNoteID, gitlab.WithContext(ctx)); err != nil {
		return fmt.Errorf("failed to delete pending review comment: %w", err)
	}

	o.io.LogInfof("✓ Deleted pending review comment %d from !%d\n", o.draftNoteID, o.mr.IID)
	return nil
}
