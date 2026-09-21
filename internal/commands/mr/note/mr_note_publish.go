package note

import (
	"context"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type publishOptions struct {
	io           *iostreams.IOStreams
	factory      cmdutils.Factory
	gitlabClient func() (*gitlab.Client, error)

	// Flags.
	message       string
	internal      bool
	reviewerState string
	yes           bool

	// Populated in complete.
	client *gitlab.Client
	mr     *gitlab.MergeRequest
	repo   glrepo.Interface
}

func NewCmdPublish(f cmdutils.Factory) *cobra.Command {
	opts := &publishOptions{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "publish [<id> | <branch>]",
		Short: "Publish all pending review comments on a merge request. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Publish every pending review comment you created on a merge request with %[1]sglab mr note create --draft%[1]s. Only your own pending comments are published; other reviewers' pending comments are unaffected.

			Use %[1]s--message%[1]s to add a summary note to the merge request when publishing, and %[1]s--internal%[1]s to restrict that summary to project members with at least the Reporter role.

			Use %[1]s--reviewer-state%[1]s to set your review state on the merge request. Neither state records a formal approval; use %[1]sglab mr approve%[1]s to approve.

			Unless you pass %[1]s--yes%[1]s, the command shows the number of pending comments and prompts you to confirm. When not running interactively, %[1]s--yes%[1]s is required. If there are no pending comments, the command exits with an error.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Publish your pending review comments on merge request 123
			glab mr note publish 123

			# Publish the current branch's pending review comments
			glab mr note publish

			# Publish with a summary note and request changes
			glab mr note publish 123 -m "A few blockers, see the comments." --reviewer-state requested_changes

			# Publish without confirmation
			glab mr note publish 123 --yes
		`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validateFlags(); err != nil {
				return err
			}
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.message, "message", "m", "", "Summary note to add to the merge request when publishing.")
	fl.BoolVar(&opts.internal, "internal", false, "Mark the summary note as internal. Requires --message.")
	fl.Var(
		cmdutils.NewEnumValue([]string{"requested_changes", "reviewed"}, "", &opts.reviewerState),
		"reviewer-state", "Set the review state after publishing: requested_changes, reviewed. Does not record a formal approval.",
	)
	fl.BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt.")

	return cmd
}

func (o *publishOptions) complete(cmd *cobra.Command, args []string) error {
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

	return nil
}

func (o *publishOptions) validateFlags() error {
	if o.message != "" && strings.TrimSpace(o.message) == "" {
		return fmt.Errorf("--message cannot be empty")
	}
	if o.internal && strings.TrimSpace(o.message) == "" {
		return fmt.Errorf("--internal requires --message")
	}
	return nil
}

func (o *publishOptions) run(ctx context.Context) error {
	if !o.yes && !o.io.PromptEnabled() {
		return cmdutils.FlagError{Err: fmt.Errorf("--yes required when not running interactively")}
	}

	listOpts := &gitlab.ListDraftNotesOptions{ListOptions: gitlab.ListOptions{PerPage: api.MaxPerPage}}
	drafts, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.DraftNote, *gitlab.Response, error) {
		return o.client.DraftNotes.ListDraftNotes(o.repo.FullName(), o.mr.IID, listOpts, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return fmt.Errorf("failed to list pending review comments: %w", err)
	}
	if len(drafts) == 0 {
		return fmt.Errorf("no pending review comments on !%d", o.mr.IID)
	}

	if !o.yes {
		var confirmed bool
		if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Publish %s?", utils.Pluralize(len(drafts), "pending review comment"))); err != nil {
			return err
		}
		if !confirmed {
			o.io.LogInfo("Aborted.")
			return nil
		}
	}

	publishOpts := &gitlab.PublishAllDraftNotesOptions{}
	if message := strings.TrimSpace(o.message); message != "" {
		publishOpts.Note = new(message)
	}
	if o.internal {
		publishOpts.Internal = new(true)
	}
	if o.reviewerState != "" {
		publishOpts.ReviewerState = new(o.reviewerState)
	}

	//nolint:staticcheck // PublishAllDraftNotesWithOptions is the only way to pass note/internal/reviewer_state in SDK v3; options merge into PublishAllDraftNotes in 4.0.
	_, err = o.client.DraftNotes.PublishAllDraftNotesWithOptions(
		o.repo.FullName(),
		o.mr.IID,
		publishOpts,
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to publish pending review comments: %w", err)
	}

	o.io.LogInfof("✓ Published %s. %s\n", utils.Pluralize(len(drafts), "pending review comment"), o.mr.WebURL)
	return nil
}
