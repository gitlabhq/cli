package update

import (
	"context"
	"errors"
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

type options struct {
	io           *iostreams.IOStreams
	factory      cmdutils.Factory
	gitlabClient func() (*gitlab.Client, error)

	// Flags.
	message string
	attach  []string

	// Populated in complete.
	client      *gitlab.Client
	mr          *gitlab.MergeRequest
	repo        glrepo.Interface
	draftNoteID int64
	position    *gitlab.NotePosition
	body        string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "update [<id> | <branch>] <draft-note-id>",
		Short: "Update the body of one of your pending review comments. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Replace the body of a pending review comment before you publish the review. %[1]s<draft-note-id>%[1]s is the ID printed by %[1]sglab mr note draft create%[1]s or listed by %[1]sglab mr note draft list%[1]s.

			You can change only the body. A pending diff comment stays on the same file and lines. Pending comments on images cannot be updated from the command line, because their position cannot be preserved.

			%[1]s--attach%[1]s uploads a file and references it at the end of the comment. Repeat the flag for more than one file, or pass %[1]s-%[1]s to read the file from standard input. Without %[1]s--message%[1]s the references are added to the body the comment already has, instead of replacing it.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Update pending review comment 456 on merge request 123
			glab mr note draft update 123 456 -m "Revised comment"

			# Update a pending review comment on the current branch's merge request, composing in an editor
			glab mr note draft update 456

			# Pipe the new body from stdin
			echo "new body" | glab mr note draft update 123 456

			# Add a screenshot to the existing comment body
			glab mr note draft update 123 456 --attach ./screenshot.png
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

	cmd.Flags().StringVarP(&opts.message, "message", "m", "", "New comment body. If omitted, opens an editor or reads from stdin.")
	cmdutils.AddAttachFlag(cmd, &opts.attach, "comment")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
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

	mr, repo, err := mrutils.MRFromArgs(cmd.Context(), o.factory, args[:len(args)-1], "any")
	if err != nil {
		return err
	}
	o.mr = mr
	o.repo = repo

	draft, err := mrutils.GetDraftNote(cmd.Context(), client, repo.FullName(), mr.IID, draftNoteID)
	if err != nil {
		return err
	}
	if mrutils.HasFilePosition(draft.Position) {
		// The client does not decode image coordinates, so re-sending the
		// position would drop them. Fail before prompting for a body.
		if draft.Position.PositionType == "image" {
			return fmt.Errorf("pending review comment %d is on an image and cannot be updated without losing its position", draftNoteID)
		}
		o.position = draft.Position
	}

	switch {
	case strings.TrimSpace(o.message) != "":
		o.body = o.message
	case len(o.attach) == 0:
		o.body, err = mrutils.NoteBodyFromStdinOrEditor(cmd.Context(), o.io, o.factory.Config)
		if err != nil {
			return err
		}
	default:
		o.body = draft.Note
	}

	return nil
}

func (o *options) validate() error {
	if strings.TrimSpace(o.body) == "" && len(o.attach) == 0 {
		return errors.New("aborted: comment has an empty message")
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	body, err := cmdutils.AppendAttachments(ctx, o.io, o.client, o.repo.FullName(), o.body, o.attach)
	if err != nil {
		return err
	}

	updateOpts := &gitlab.UpdateDraftNoteOptions{Note: &body}
	// GitLab before 19.5 clears the stored position when an update omits it,
	// which turns a diff comment into a general one, so send it back.
	if o.position != nil {
		updateOpts.Position = positionOptions(o.position)
	}

	if _, _, err := o.client.DraftNotes.UpdateDraftNote(o.repo.FullName(), o.mr.IID, o.draftNoteID, updateOpts, gitlab.WithContext(ctx)); err != nil {
		return fmt.Errorf("failed to update pending review comment: %w", err)
	}

	o.io.LogInfof("%d\n", o.draftNoteID)
	return nil
}

func positionOptions(pos *gitlab.NotePosition) *gitlab.PositionOptions {
	opts := &gitlab.PositionOptions{
		BaseSHA:      nonZero(pos.BaseSHA),
		StartSHA:     nonZero(pos.StartSHA),
		HeadSHA:      nonZero(pos.HeadSHA),
		PositionType: nonZero(pos.PositionType),
		NewPath:      nonZero(pos.NewPath),
		OldPath:      nonZero(pos.OldPath),
		NewLine:      nonZero(pos.NewLine),
		OldLine:      nonZero(pos.OldLine),
	}
	if lr := pos.LineRange; lr != nil && lr.StartRange != nil && lr.EndRange != nil {
		opts.LineRange = &gitlab.LineRangeOptions{
			Start: linePositionOptions(lr.StartRange),
			End:   linePositionOptions(lr.EndRange),
		}
	}
	return opts
}

func linePositionOptions(lp *gitlab.LinePosition) *gitlab.LinePositionOptions {
	return &gitlab.LinePositionOptions{
		LineCode: nonZero(lp.LineCode),
		Type:     nonZero(lp.Type),
		OldLine:  nonZero(lp.OldLine),
		NewLine:  nonZero(lp.NewLine),
	}
}

func nonZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}
