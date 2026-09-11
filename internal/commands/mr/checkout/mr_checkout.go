package checkout

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
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	// flag values
	branch   string
	track    bool
	upstream string
	force    bool

	// dependencies captured from factory
	factory      cmdutils.Factory
	io           *iostreams.IOStreams
	gr           git.GitRunner
	gitlabClient func() (*gitlab.Client, error)
	remotes      func() (glrepo.Remotes, error)
	config       func() config.Config

	// resolved by complete before invoking run
	mr       *gitlab.MergeRequest
	baseRepo glrepo.Interface
}

func NewCmdCheckout(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		factory:      f,
		io:           f.IO(),
		gr:           f.GitRunner(),
		gitlabClient: f.GitLabClient,
		remotes:      f.Remotes,
		config:       f.Config,
	}

	cmd := &cobra.Command{
		Use:   "checkout [<id> | <branch> | <url>]",
		Short: "Check out an open merge request.",
		Long: heredoc.Docf(`
			Defaults to the currently checked-out branch. Use %[1]s--branch%[1]s to
			override the local branch name used for the checkout.
		`, "`"),
		Example: heredoc.Doc(`
			glab mr checkout 1
			glab mr checkout branch
			glab mr checkout 12 --branch todo-fix
			glab mr checkout new-feature --set-upstream-to=upstream/main
			glab mr checkout https://gitlab.com/gitlab-org/cli/-/merge_requests/1234

			# Uses the checked-out branch
			glab mr checkout`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := opts.complete(ctx, args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(ctx)
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.branch, "branch", "b", "", "Check out merge request with name <branch>.")
	fl.BoolVarP(&opts.track, "track", "t", true, "Set checked out branch to track the remote branch.")
	_ = fl.MarkDeprecated("track", "Now enabled by default")
	fl.StringVarP(&opts.upstream, "set-upstream-to", "u", "", "Set tracking of checked-out branch to [REMOTE/]BRANCH.")
	fl.BoolVarP(&opts.force, "force", "f", false, "Reset local branch to remote when they have diverged. Refuses if working tree has changes that would be lost.")
	return cmd
}

func (o *options) complete(ctx context.Context, args []string) error {
	mr, baseRepo, err := mrutils.MRFromArgs(ctx, o.factory, args, "any")
	if err != nil {
		return err
	}
	if mr.SHA == "" {
		return fmt.Errorf("merge request !%d has no head commit", mr.IID)
	}
	o.mr = mr
	o.baseRepo = baseRepo

	if o.branch == "" {
		o.branch = o.mr.SourceBranch
	}

	return nil
}

func (o *options) validate() error {
	if o.upstream != "" {
		if val := strings.Split(o.upstream, "/"); len(val) > 1 {
			// Verify that we have the remote set
			repo, err := o.remotes()
			if err != nil {
				return err
			}
			_, err = repo.FindByName(val[0])
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	var mrRef string
	var mrProject *gitlab.Project

	mrProject, err = api.GetProject(client, o.mr.SourceProjectID)
	if err != nil {
		// If we don't have access to the source project, let's try the target project
		mrProject, err = api.GetProject(client, o.mr.TargetProjectID)
		if err != nil {
			return err
		}

		// We found the target project, let's find the ref another way
		mrRef = fmt.Sprintf("refs/merge-requests/%d/head", o.mr.IID)
	} else {
		mrRef = fmt.Sprintf("refs/heads/%s", o.mr.SourceBranch)
	}

	// Get the appropriate remote URL based on user's git_protocol preference
	gitProtocol, err := o.config().Get(o.baseRepo.RepoHost(), "git_protocol")
	if err != nil {
		return err
	}
	repoURL := glrepo.RemoteURL(mrProject, gitProtocol)

	localSHA := o.localBranchSHA()
	switch {
	case localSHA == o.mr.SHA:
		o.io.LogInfof("Branch %q already at merge request head, skipping fetch.\n", o.branch)
	case localSHA == "" && o.mrHeadIsLocal():
		if _, err := o.gr.Git("branch", o.branch, o.mr.SHA); err != nil {
			return err
		}
		o.io.LogInfof("Created branch %q from local commit, skipping fetch.\n", o.branch)
	default:
		fetchRefSpec := fmt.Sprintf("%s:%s", mrRef, o.branch)
		if err := o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "fetch", repoURL, fetchRefSpec); err != nil {
			// Remote diverged from local. Fall back to fetching just the ref (FETCH_HEAD only).
			if err := o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "fetch", repoURL, mrRef); err != nil {
				return err
			}
			if err := o.resolveDivergence(ctx); err != nil {
				return err
			}
		}
	}

	// .remote is needed for `git pull` to work
	// .pushRemote is needed for `git push` to work, if user has set `remote.pushDefault`.
	// see https://git-scm.com/docs/git-config#Documentation/git-config.txt-branchltnamegtremote
	if _, err := o.gr.Git("config", fmt.Sprintf("branch.%s.remote", o.branch), repoURL); err != nil {
		return err
	}
	if o.mr.AllowCollaboration {
		if _, err := o.gr.Git("config", fmt.Sprintf("branch.%s.pushRemote", o.branch), repoURL); err != nil {
			return err
		}
	}
	if _, err := o.gr.Git("config", fmt.Sprintf("branch.%s.merge", o.branch), mrRef); err != nil {
		return err
	}

	if err := o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "checkout", o.branch); err != nil {
		return fmt.Errorf("could not checkout branch %q: %w", o.branch, err)
	}

	if o.upstream != "" {
		if _, err := o.gr.Git("branch", "--set-upstream-to", o.upstream); err != nil {
			return err
		}
	}
	return nil
}

// localBranchSHA returns the commit refs/heads/<branch> points at, or "" when
// the branch does not exist.
func (o *options) localBranchSHA() string {
	sha, err := o.gr.Git("rev-parse", "--verify", "refs/heads/"+o.branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sha)
}

// mrHeadIsLocal reports whether the merge request head commit is already in
// the object store, so a branch can be created from it without fetching.
func (o *options) mrHeadIsLocal() bool {
	_, err := o.gr.Git("rev-parse", "--verify", o.mr.SHA+"^{commit}")
	return err == nil
}

// resolveDivergence is called after the fallback fetch wrote FETCH_HEAD but the
// local branch ref was not updated. Decides whether to reset, ask the user, or
// abort. The force flag bypasses the user prompt; otherwise an interactive
// prompt confirms (TTY) or a FlagError is returned (non-TTY).
func (o *options) resolveDivergence(ctx context.Context) error {
	diverged, err := o.localDivergesFromFetchHead()
	if err != nil {
		return err
	}
	if !diverged {
		return nil
	}

	onTarget, err := o.guardReset()
	if err != nil {
		return err
	}

	if !o.force {
		if !o.io.PromptEnabled() {
			return cmdutils.FlagError{Err: fmt.Errorf("local branch %q has diverged from remote. Pass --force to reset it (discards local commits on this branch)", o.branch)}
		}
		var confirmed bool
		if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Local branch %q has diverged from remote. Reset to remote (discards local commits on this branch)?", o.branch)); err != nil {
			return err
		}
		if !confirmed {
			return cmdutils.CancelError()
		}
	}
	return o.applyReset(onTarget)
}

// localDivergesFromFetchHead reports whether the local branch ref points at a
// different commit than FETCH_HEAD. A missing local branch is treated as "no
// divergence" so downstream code surfaces any error.
func (o *options) localDivergesFromFetchHead() (bool, error) {
	localSHA := o.localBranchSHA()
	if localSHA == "" {
		return false, nil
	}
	fetchSHA, err := o.gr.Git("rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return false, err
	}
	return localSHA != strings.TrimSpace(fetchSHA), nil
}

// guardReset checks whether `git reset --hard FETCH_HEAD` is safe to run for
// the target branch. Returns whether HEAD is currently the target branch, or an
// error describing why the reset would destroy uncommitted work.
func (o *options) guardReset() (bool, error) {
	currentBranch, symErr := o.gr.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	onTarget := symErr == nil && strings.TrimSpace(currentBranch) == o.branch
	if !onTarget {
		return false, nil
	}
	unsafe, err := o.unsafeToReset()
	if err != nil {
		return false, err
	}
	if unsafe {
		return false, fmt.Errorf("working tree has changes that would be lost by reset. Commit, stash, or remove them before resetting branch %q", o.branch)
	}
	return true, nil
}

// applyReset mutates the local ref to match FETCH_HEAD. Callers must invoke
// guardReset first and use its returned onTarget value.
func (o *options) applyReset(onTarget bool) error {
	if onTarget {
		return o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "reset", "--hard", "FETCH_HEAD")
	}
	return o.gr.GitWithIO(o.io.StdOut, o.io.StdErr, "branch", "-f", o.branch, "FETCH_HEAD")
}

// unsafeToReset reports whether `git reset --hard FETCH_HEAD` would
// silently destroy uncommitted work. Two conditions trigger it:
//  1. Tracked files with modified or staged changes (reset --hard discards them).
//  2. Untracked files whose paths appear in FETCH_HEAD's tree (reset --hard
//     overwrites them silently — unlike `git checkout`, which refuses).
//
// Unrelated untracked files (local notes, build artifacts) are NOT a blocker
// since reset --hard leaves them alone.
func (o *options) unsafeToReset() (bool, error) {
	tracked, err := o.gr.Git("diff", "--name-only", "HEAD")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(tracked) != "" {
		return true, nil
	}
	untracked, err := o.gr.Git("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(untracked) == "" {
		return false, nil
	}
	incoming, err := o.gr.Git("ls-tree", "-r", "--name-only", "FETCH_HEAD")
	if err != nil {
		return false, err
	}
	incomingPaths := map[string]struct{}{}
	for line := range strings.SplitSeq(incoming, "\n") {
		if line != "" {
			incomingPaths[line] = struct{}{}
		}
	}
	for line := range strings.SplitSeq(untracked, "\n") {
		if line == "" {
			continue
		}
		if _, conflict := incomingPaths[line]; conflict {
			return true, nil
		}
	}
	return false, nil
}
