package create

import (
	"context"
	"errors"
	"fmt"
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
	message  string
	attach   []string
	reply    string
	filePath string
	line     mrutils.LineRange
	oldLine  int

	// Populated in complete.
	client *gitlab.Client
	mr     *gitlab.MergeRequest
	repo   glrepo.Interface
	body   string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "create [<id> | <branch>]",
		Short: "Add a pending review comment to a merge request. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Add a comment to your pending review. The comment stays visible only to you until you publish the review with %[1]sglab mr note draft publish%[1]s or submit it from the merge request page.

			On success, the command prints the ID of the pending comment. Pass that ID to %[1]sglab mr note draft update%[1]s or %[1]sglab mr note draft delete%[1]s.

			Use %[1]s--file%[1]s to place the comment on a specific file in the latest merge request diff version. Combine with %[1]s--line%[1]s (new side) or %[1]s--old-line%[1]s (old/removed side) to target a specific line. Omit both flags for a file-level comment.

			Use %[1]s--reply%[1]s to reply to an existing discussion thread. The value can be a full discussion ID or a unique prefix of at least 8 characters. Find discussion IDs with %[1]sglab mr note list%[1]s.

			The flag rules are:

			- %[1]s--line%[1]s and %[1]s--old-line%[1]s require %[1]s--file%[1]s, and cannot be used together.
			- %[1]s--file%[1]s and %[1]s--reply%[1]s are mutually exclusive.

			%[1]s--attach%[1]s uploads a file and references it at the end of the comment. Repeat the flag for more than one file, or pass %[1]s-%[1]s to read the file from standard input. The upload happens immediately, even though the comment stays pending. An attachment is content on its own, so a comment with only %[1]s--attach%[1]s neither prompts nor reads a body from stdin.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Add a pending comment to merge request 123
			glab mr note draft create 123 -m "Consider renaming this."

			# Add a pending comment to the current branch's merge request
			glab mr note draft create -m "Looks good overall."

			# Pipe the body from stdin
			echo "Needs a test." | glab mr note draft create 123

			# Add a pending diff comment on line 42 of main.go
			glab mr note draft create 123 --file main.go --line 42 -m "Off-by-one?"

			# Add a pending diff comment on lines 10-15
			glab mr note draft create 123 --file main.go --line 10:15 -m "Extract this block."

			# Add a pending diff comment on a removed line
			glab mr note draft create 123 --file main.go --old-line 7 -m "Why was this removed?"

			# Add a pending reply to an existing discussion thread
			glab mr note draft create 123 --reply abc12345 -m "Agreed."

			# Keep the ID of the pending comment for a later update
			id=$(glab mr note draft create 123 -m "First pass")
		`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.message, "message", "m", "", "Comment message. If omitted, opens an editor or reads from stdin.")
	fl.StringVar(&opts.reply, "reply", "", "Reply to an existing discussion. Accepts a full discussion ID or a unique prefix of at least 8 characters.")
	fl.StringVar(&opts.filePath, "file", "", "File path for a diff comment, like <path/to/file>. Targets the latest merge request diff version.")
	fl.Var(&opts.line, "line", "Line in the new version. A single line number, like 42, or a range, like 10:15.")
	fl.IntVar(&opts.oldLine, "old-line", 0, "Line in the old version, for commenting on a removed line.")
	cmdutils.AddAttachFlag(cmd, &opts.attach, "comment")

	cmd.MarkFlagsMutuallyExclusive("reply", "file")
	cmd.MarkFlagsMutuallyExclusive("line", "old-line")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	o.client = client

	mr, repo, err := mrutils.MRFromArgs(cmd.Context(), o.factory, args, "any")
	if err != nil {
		return err
	}
	o.mr = mr
	o.repo = repo

	o.body = o.message
	// The attachment may itself be what is on stdin, so --attach suppresses
	// reading the body from stdin or an editor.
	if strings.TrimSpace(o.body) == "" && len(o.attach) == 0 {
		o.body, err = mrutils.NoteBodyFromStdinOrEditor(cmd.Context(), o.io, o.factory.Config)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(o.body) == "" && len(o.attach) == 0 {
		return errors.New("aborted: comment has an empty message")
	}

	return nil
}

// validate checks only flag values, so it runs before complete to reject bad
// input before any API call or editor prompt.
func (o *options) validate() error {
	if o.reply != "" && len(o.reply) < 8 {
		return fmt.Errorf("discussion ID prefix must be at least 8 characters, got %d", len(o.reply))
	}
	if (o.line.Start != 0 || o.oldLine != 0) && o.filePath == "" {
		return errors.New("--line and --old-line require --file")
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	createOpts := &gitlab.CreateDraftNoteOptions{}

	switch {
	case o.filePath != "":
		position, err := mrutils.DiffPosition(ctx, o.client, o.repo.FullName(), o.mr.IID, o.filePath, o.line, o.oldLine)
		if err != nil {
			return err
		}
		createOpts.Position = position
	case o.reply != "":
		discussionID, err := mrutils.ResolveDiscussionID(ctx, o.client, o.repo.FullName(), o.mr.IID, o.reply)
		if err != nil {
			return err
		}
		createOpts.InReplyToDiscussionID = &discussionID
	}

	// Uploads cannot be undone, so they run only after the position and reply
	// target have resolved.
	body, err := cmdutils.AppendAttachments(ctx, o.io, o.client, o.repo.FullName(), o.body, o.attach)
	if err != nil {
		return err
	}
	createOpts.Note = &body

	draft, _, err := o.client.DraftNotes.CreateDraftNote(o.repo.FullName(), o.mr.IID, createOpts, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to create pending review comment: %w", err)
	}

	o.io.LogInfof("%d\n", draft.ID)
	return nil
}
