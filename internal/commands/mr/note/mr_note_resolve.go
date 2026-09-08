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
	mrArgs           []string
	discussionPrefix string
	client           *gitlab.Client
	mr               *gitlab.MergeRequest
	repo             glrepo.Interface
	discussionID     string
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
		Use:   fmt.Sprintf("%s [<id> | <branch>] <discussion-id>", action),
		Short: fmt.Sprintf("%s a discussion on a merge request. (EXPERIMENTAL)", capitalize(action)),
		Long: heredoc.Docf(`
			%s a discussion on a merge request.

			The identifier can be one of the following:

			- Discussion ID: full 40-character hex string or an 8+ character prefix
			- Note ID: integer note ID (looks up the parent discussion automatically)

			If a prefix matches multiple discussions, an error is returned with the ambiguous matches.
		`, capitalize(action)) + text.ExperimentalString,
		Example: heredoc.Docf(`
			# %s a discussion on merge request 123 by prefix
			glab mr note %s 123 abc12345

			# %s a discussion by note ID
			glab mr note %s 3107030349

			# %s a discussion by prefix (8+ chars, auto-detects merge request from branch)
			glab mr note %s abc12345

			# %s a discussion by full ID
			glab mr note %s abc12345deadbeef1234567890abcdef12345678
		`, capitalize(action), action, capitalize(action), action, capitalize(action), action, capitalize(action), action),
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd.Context(), args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	return cmd
}

func (o *resolveOptions) complete(ctx context.Context, args []string) error {
	o.discussionPrefix = args[len(args)-1]
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

	// Resolve the discussion ID from the prefix or note ID.
	if noteID, parseErr := strconv.ParseInt(o.discussionPrefix, 10, 64); parseErr == nil {
		discussions, listErr := mrutils.ListAllDiscussions(ctx, client, repo.FullName(), mr.IID, &gitlab.ListMergeRequestDiscussionsOptions{})
		if listErr != nil {
			return fmt.Errorf("failed to list discussions: %w", listErr)
		}
		o.discussionID, _, err = mrutils.FindNoteInDiscussions(discussions, noteID)
		if err != nil {
			return fmt.Errorf("note %d not found in merge request !%d: %w", noteID, mr.IID, err)
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
