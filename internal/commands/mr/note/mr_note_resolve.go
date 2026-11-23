package note

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

type resolveOptions struct {
	io           *iostreams.IOStreams
	factory      cmdutils.Factory
	gitlabClient func() (*gitlab.Client, error)
	resolve      bool
	action       string
	past         string

	// Populated in complete.
	repoOverride     string
	mrArgs           []string
	discussionPrefix string

	// Populated in run.
	client       *gitlab.Client
	mr           *gitlab.MergeRequest
	repo         glrepo.Interface
	discussionID string
}

func NewCmdResolve(f cmdutils.Factory) *cobra.Command {
	return newResolveCmd(f, true)
}

func NewCmdReopen(f cmdutils.Factory) *cobra.Command {
	return newResolveCmd(f, false)
}

func newResolveCmd(f cmdutils.Factory, resolve bool) *cobra.Command {
	action := "resolve"
	past := "resolved"
	if !resolve {
		action = "reopen"
		past = "reopened"
	}

	opts := &resolveOptions{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
		resolve:      resolve,
		action:       action,
		past:         past,
	}

	cmd := &cobra.Command{
		// fang moves bracketed args to the end unless one trails them
		Use:   fmt.Sprintf("%s [<id> | <branch>] <discussion-id> [-R <repo>]", action),
		Short: fmt.Sprintf("%s a discussion on a merge request. (EXPERIMENTAL)", capitalize(action)),
		Long: heredoc.Docf(`
			%s a discussion on a merge request.

			The identifier can be one of the following:

			- Discussion ID: full 40-character hex string or an 8+ character prefix
			- Note ID: integer note ID (looks up the parent discussion automatically)

			The merge request comes first, then the discussion. Without a merge request, the merge request for the current branch is used. With --repo, the merge request is required.

			If a prefix matches multiple discussions, an error is returned with the ambiguous matches.
		`, capitalize(action)) + text.ExperimentalString,
		Example: heredoc.Docf(`
			# %[1]s a discussion on merge request 123 by prefix
			glab mr note %[2]s 123 abc12345

			# %[1]s a discussion on merge request 123 in another repository
			glab mr note %[2]s 123 abc12345 -R OWNER/REPO

			# %[1]s a discussion by note ID
			glab mr note %[2]s 3107030349

			# %[1]s a discussion by prefix (8+ chars, auto-detects merge request from branch)
			glab mr note %[2]s abc12345

			# %[1]s a discussion by full ID
			glab mr note %[2]s abc12345deadbeef1234567890abcdef12345678
		`, capitalize(action), action),
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

	return cmd
}

func (o *resolveOptions) complete(cmd *cobra.Command, args []string) error {
	repoOverride, err := cmd.Flags().GetString("repo")
	if err != nil {
		return err
	}
	o.repoOverride = repoOverride
	o.discussionPrefix = args[len(args)-1]
	if len(args) == 2 {
		o.mrArgs = args[:1]
	}

	return nil
}

func (o *resolveOptions) validate() error {
	if o.repoOverride != "" && len(o.mrArgs) == 0 {
		return &cmdutils.FlagError{Err: fmt.Errorf("a merge request ID or branch is required with --repo: glab mr note %s <id> <discussion-id> -R %s", o.action, o.repoOverride)}
	}
	return nil
}

func (o *resolveOptions) lookup(ctx context.Context) error {
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

	// Resolve the discussion ID from the prefix or note ID.
	if noteID, parseErr := strconv.ParseInt(o.discussionPrefix, 10, 64); parseErr == nil {
		o.discussionID, _, err = mrutils.FindNoteInDiscussionPaginated(ctx, client, repo.FullName(), mr.IID, noteID)
		if err != nil {
			if errors.Is(err, mrutils.ErrNoteNotFound) {
				return fmt.Errorf("note %d not found in merge request !%d", noteID, mr.IID)
			}
			return fmt.Errorf("failed to look up note %d in merge request !%d: %w", noteID, mr.IID, err)
		}
	} else {
		o.discussionID, err = mrutils.ResolveDiscussionID(ctx, client, repo.FullName(), mr.IID, o.discussionPrefix)
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *resolveOptions) run(ctx context.Context) error {
	if err := o.lookup(ctx); err != nil {
		return err
	}

	_, _, err := o.client.Discussions.ResolveMergeRequestDiscussion(
		o.repo.FullName(),
		o.mr.IID,
		o.discussionID,
		&gitlab.ResolveMergeRequestDiscussionOptions{
			Resolved: &o.resolve,
		},
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to %s discussion: %w", o.action, err)
	}

	o.io.LogInfof("✓ Discussion %s (%s in !%d)\n", o.past, mrutils.TruncateDiscussionID(o.discussionID), o.mr.IID)
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
