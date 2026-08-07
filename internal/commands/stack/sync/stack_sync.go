package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/auth"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/create"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io             *iostreams.IOStreams
	stack          git.Stack
	target         glrepo.Interface
	source         glrepo.Interface
	labClient      *gitlab.Client
	baseRepo       func() (glrepo.Interface, error)
	remotes        func() (glrepo.Remotes, error)
	user           gitlab.User
	noVerify       bool
	updateBase     bool
	skipMRCreation bool
	assignees      []string
	assigneeIDs    *[]int64
	labels         []string
	reviewers      []string
	reviewerIDs    *[]int64
}

// max string size for MR title is ~255, but we'll add a "..."
const maxMRTitleSize = 252

const (
	BranchIsBehind    = "Your branch is behind"
	BranchHasDiverged = "have diverged"
	NothingToCommit   = "nothing to commit"
	mergedStatus      = "merged"
	closedStatus      = "closed"
)

func NewCmdSyncStack(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	opts := &options{
		io:       f.IO(),
		remotes:  f.Remotes,
		baseRepo: f.BaseRepo,
	}

	stackSaveCmd := &cobra.Command{
		Use:   "sync",
		Short: `Sync and submit progress on a stacked diff. (EXPERIMENTAL)`,
		Long: heredoc.Doc(`Sync and submit progress on a stacked diff. This command runs these steps:

1. Optional. If working in a fork, select whether to push to the fork,
   or the upstream repository.
1. Optional. If --update-base is set, rebases the entire stack onto the
   latest version of the base branch.
1. Pushes any amended changes to their merge requests.
1. Rebases any changes that happened previously in the stack.
1. Creates merge requests for branches that don't have one yet,
   unless --skip-mr-creation is set.
1. Removes any branches that were already merged, or with a closed merge request.
` + text.ExperimentalString),
		Example: heredoc.Doc(`
			glab stack sync
			glab stack sync --no-verify
			glab stack sync --update-base
			glab stack sync --skip-mr-creation
			glab stack sync --assignee user1,user2
			glab stack sync --label bug,priority::high
			glab stack sync --reviewer user1 --reviewer user2`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.io.StartSpinner("Syncing")
			defer opts.io.StopSpinner("")

			var gr git.StandardGitCommand

			return opts.run(cmd.Context(), f, gr)
		},
	}

	fl := stackSaveCmd.Flags()
	fl.BoolVar(&opts.noVerify, "no-verify", false, "Bypass the pre-push hook. (See githooks(5) for more information.)")
	fl.BoolVar(&opts.updateBase, "update-base", false, "Rebase the stack onto the latest version of the base branch.")
	fl.BoolVar(&opts.skipMRCreation, "skip-mr-creation", false, "Skip creating merge requests for branches that don't have one yet.")
	fl.StringSliceVarP(&opts.assignees, "assignee", "a", []string{}, "Assign merge request to people by their `usernames`. Multiple usernames can be comma-separated or specified by repeating the flag.")
	fl.StringSliceVarP(&opts.labels, "label", "l", []string{}, "Add label by `name`. Multiple labels can be comma-separated or specified by repeating the flag.")
	fl.StringSliceVar(&opts.reviewers, "reviewer", []string{}, "Request review from users by their `usernames`. Multiple usernames can be comma-separated or specified by repeating the flag.")

	return stackSaveCmd
}

func (o *options) run(ctx context.Context, f cmdutils.Factory, gr git.GitRunner) error {
	client, err := auth.GetAuthenticatedClient(f.Config(), f.GitLabClient, f.IO())
	if err != nil {
		return fmt.Errorf("error authorizing with GitLab: %w", err)
	}
	o.labClient = client

	o.io.StopSpinner("")

	repo, err := f.BaseRepo()
	if err != nil {
		return fmt.Errorf("error determining base repo: %w", err)
	}

	// This prompts the user for the head repo if they're in a fork,
	// allowing them to choose between their fork and the original repository
	source, err := create.ResolvedHeadRepo(ctx, f)()
	if err != nil {
		return fmt.Errorf("error determining head repo: %w", err)
	}

	o.io.StartSpinner("Syncing")

	stack, err := getStack()
	if err != nil {
		return fmt.Errorf("error getting current stack: %w", err)
	}

	user, _, err := client.Users.CurrentUser()
	if err != nil {
		return fmt.Errorf("error getting current user: %w", err)
	}

	o.stack = stack
	o.target = repo
	o.source = source
	o.user = *user

	if err := o.validate(); err != nil {
		return err
	}

	if err := o.complete(client); err != nil {
		return err
	}

	err = fetchOrigin(gr)
	if err != nil {
		return err
	}

	pushAfterSync := false

	if o.updateBase {
		baseBranch, err := stack.BaseBranch(gr)
		if err != nil {
			return fmt.Errorf("error getting base branch: %w", err)
		}

		remoteBase := git.DefaultRemote + "/" + baseBranch
		fmt.Print(progressString(o.io, "Rebasing stack onto "+remoteBase+"..."))

		err = rebaseWithUpdateRefs(o.io, remoteBase, &stack, gr)
		if err != nil {
			return err
		}
		pushAfterSync = true
	}

	for ref := range stack.Iter() {
		status, err := branchStatus(&ref, gr)
		if err != nil {
			return fmt.Errorf("error getting branch status: %w", err)
		}

		switch {
		case strings.Contains(status, BranchIsBehind):
			err = branchBehind(o.io, &ref, gr)
			if err != nil {
				return err
			}
		case strings.Contains(status, BranchHasDiverged):
			needsPush, err := branchDiverged(o.io, &ref, &stack, gr)
			if err != nil {
				return err
			}

			if needsPush {
				pushAfterSync = true
			}
		case strings.Contains(status, NothingToCommit):
			// this is fine. we can just move on.
		default:
			return fmt.Errorf("your Git branch is ahead, but it shouldn't be; you might need to squash your commits")
		}

		if ref.MR == "" {
			if o.skipMRCreation {
				fmt.Println(progressString(o.io, ref.Branch+" has no merge request. Skipping MR creation."))
			} else {
				err := populateMR(o.io, &ref, o, client, gr)
				if err != nil {
					return err
				}
			}
		} else {
			// we found an MR. let's get the status:
			mr, _, err := mrutils.MRFromArgsWithOpts(ctx, f, []string{ref.Branch}, nil, "any")
			if err != nil {
				return fmt.Errorf("error getting merge request from branch: %w. Does it still exist?", err)
			}

			// remove the MR from the stack if it's merged
			// do not remove the MR from the stack if it is closed,
			// but alert the user
			err = removeOldMrs(o.io, &ref, mr, &stack, gr)
			if err != nil {
				return fmt.Errorf("error removing merged merge request: %w", err)
			}
		}
	}

	if pushAfterSync {
		err := forcePushAllWithLease(o, &stack, gr)
		if err != nil {
			return fmt.Errorf("error pushing branches to remote: %w", err)
		}
	}

	fmt.Print(progressString(o.io, "Sync finished!"))
	return nil
}

func filterEmpty(s []string) []string {
	result := make([]string, 0, len(s))
	for _, v := range s {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func dedupe(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	result := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}

func (o *options) validate() error {
	raw := o.assignees
	o.assignees = dedupe(filterEmpty(o.assignees))

	if len(raw) > 0 && len(o.assignees) == 0 {
		return fmt.Errorf("--assignee (-a) flag requires at least one valid username")
	}

	rawLabels := o.labels
	o.labels = dedupe(filterEmpty(o.labels))

	if len(rawLabels) > 0 && len(o.labels) == 0 {
		return fmt.Errorf("--label (-l) flag requires at least one valid label name")
	}

	rawReviewers := o.reviewers
	o.reviewers = dedupe(filterEmpty(o.reviewers))

	if len(rawReviewers) > 0 && len(o.reviewers) == 0 {
		return fmt.Errorf("--reviewer flag requires at least one valid username")
	}

	return nil
}

func (o *options) complete(client *gitlab.Client) error {
	if len(o.assignees) > 0 {
		users, err := api.UsersByNames(client, o.assignees)
		if err != nil {
			return fmt.Errorf("error resolving assignee usernames: %w", err)
		}
		o.assigneeIDs = cmdutils.IDsFromUsers(users)
	}

	if len(o.reviewers) > 0 {
		users, err := api.UsersByNames(client, o.reviewers)
		if err != nil {
			return fmt.Errorf("error resolving reviewer usernames: %w", err)
		}
		o.reviewerIDs = cmdutils.IDsFromUsers(users)
	}

	return nil
}

func getStack() (git.Stack, error) {
	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return git.Stack{}, fmt.Errorf("error getting current stack: %w", err)
	}

	stack, err := git.GatherStackRefs(title)
	if err != nil {
		return git.Stack{}, fmt.Errorf("error getting current stack references: %w", err)
	}
	return stack, nil
}

func gitPull(gr git.GitRunner) (string, error) {
	pull, err := gr.Git("pull")
	if err != nil {
		return "", err
	}
	dbg.Debug("Pulled:", pull)

	return pull, nil
}

func fetchOrigin(gr git.GitRunner) error {
	output, err := gr.Git("fetch", git.DefaultRemote)
	dbg.Debug("Fetching from remote:", output)

	if err != nil {
		return err
	}

	return nil
}

func branchStatus(ref *git.StackRef, gr git.GitRunner) (string, error) {
	checkout, err := gr.Git("checkout", ref.Branch)
	if err != nil {
		return "", err
	}
	dbg.Debug("Checked out:", checkout)

	output, err := gr.Git("status", "-uno")
	if err != nil {
		return "", err
	}
	dbg.Debug("Git status:", output)

	return output, nil
}

func rebaseWithUpdateRefs(io *iostreams.IOStreams, target string, stack *git.Stack, gr git.GitRunner) error {
	lastRef := stack.Last()

	checkout, err := gr.Git("checkout", lastRef.Branch)
	if err != nil {
		return err
	}
	dbg.Debug("Checked out:", checkout)

	rebase, err := gr.Git("rebase", "--fork-point", "--update-refs", target)
	if err != nil {
		return errors.New(errorString(
			io,
			"could not rebase onto "+target+", likely due to a merge conflict.",
			"Fix the issues with Git and run `glab stack sync` again.",
		))
	}
	dbg.Debug("Rebased:", rebase)

	return nil
}

func forcePushAllWithLease(opts *options, stack *git.Stack, gr git.GitRunner) error {
	opts.io.LogInfo(progressString(
		opts.io,
		"Updating branches:",
		strings.Join(stack.Branches(), ", "),
	))

	pushArgs := []string{"push", git.DefaultRemote, "--force-with-lease"}
	if opts.noVerify {
		pushArgs = append(pushArgs, "--no-verify")
	}
	pushArgs = append(pushArgs, stack.Branches()...)

	if err := gr.GitWithIO(opts.io.StdOut, opts.io.StdErr, pushArgs...); err != nil {
		return err
	}

	opts.io.LogInfo(strings.TrimSuffix(progressString(opts.io, "Push succeeded"), "\n"))
	return nil
}

func createMR(client *gitlab.Client, opts *options, ref *git.StackRef, gr git.GitRunner) (*gitlab.MergeRequest, error) {
	targetProject, err := opts.target.Project(client)
	if err != nil {
		return &gitlab.MergeRequest{}, fmt.Errorf("error getting target project: %w", err)
	}

	pushArgs := []string{"push", "--set-upstream", git.DefaultRemote}
	if opts.noVerify {
		pushArgs = append(pushArgs, "--no-verify")
	}
	pushArgs = append(pushArgs, ref.Branch)

	if err = gr.GitWithIO(opts.io.StdOut, opts.io.StdErr, pushArgs...); err != nil {
		return &gitlab.MergeRequest{}, fmt.Errorf("error pushing branch: %w", err)
	}

	var previousBranch string
	if ref.IsFirst() {
		// Point to the base branch
		previousBranch, err = opts.stack.BaseBranch(gr)
		if err != nil {
			return &gitlab.MergeRequest{}, fmt.Errorf("error getting base branch: %w", err)
		}

		if !git.RemoteBranchExists(previousBranch, gr) {
			return &gitlab.MergeRequest{}, fmt.Errorf("branch %q does not exist on remote %q. Please push the branch to the remote before syncing",
				previousBranch,
				git.DefaultRemote)
		}

	} else {
		// if we have a previous branch, let's point to that
		previousBranch = opts.stack.Refs[ref.Prev].Branch
	}

	title, _, _ := strings.Cut(ref.Description, "\n")
	title = strings.TrimSpace(title)
	if len(title) > maxMRTitleSize {
		title = title[:maxMRTitleSize-3] + "..."
	}
	description := ref.Body()

	l := &gitlab.CreateMergeRequestOptions{
		Title:              new(title),
		Description:        new(description),
		SourceBranch:       new(ref.Branch),
		TargetBranch:       new(previousBranch),
		RemoveSourceBranch: new(true),
		TargetProjectID:    new(targetProject.ID),
	}

	if opts.assigneeIDs != nil {
		l.AssigneeIDs = opts.assigneeIDs
	} else {
		l.AssigneeID = new(opts.user.ID)
	}

	if len(opts.labels) > 0 {
		l.Labels = (*gitlab.LabelOptions)(&opts.labels)
	}

	if opts.reviewerIDs != nil {
		l.ReviewerIDs = opts.reviewerIDs
	}

	mr, _, err := client.MergeRequests.CreateMergeRequest(opts.source.FullName(), l)
	if err != nil {
		return &gitlab.MergeRequest{}, fmt.Errorf("error creating merge request with the API: %w", err)
	}

	return mr, nil
}

func removeOldMrs(io *iostreams.IOStreams, ref *git.StackRef, mr *gitlab.MergeRequest, stack *git.Stack, gr git.GitRunner) error {
	switch mr.State {
	case mergedStatus:
		progress := fmt.Sprintf("Merge request !%v has merged. Removing reference...", mr.IID)
		fmt.Println(progressString(io, progress))

		err := stack.RemoveRef(*ref, gr)
		if err != nil {
			return err
		}
	case closedStatus:
		progress := fmt.Sprintf("MR !%v has closed", mr.IID)
		fmt.Println(progressString(io, progress))
	}
	return nil
}

func errorString(io *iostreams.IOStreams, lines ...string) string {
	redCheck := io.Color().Red("✘")

	title := lines[0]
	body := strings.Join(lines[1:], "\n  ")

	return fmt.Sprintf("\n%s %s \n  %s", redCheck, title, body)
}

func progressString(io *iostreams.IOStreams, lines ...string) string {
	blueDot := io.Color().ProgressIcon()
	title := lines[0]

	var body string

	if len(lines) > 1 {
		body = strings.Join(lines[1:], "\n  ")
		return fmt.Sprintf("\n%s %s \n  %s", blueDot, title, body)
	}
	return fmt.Sprintf("\n%s %s\n", blueDot, title)
}

func branchDiverged(io *iostreams.IOStreams, ref *git.StackRef, stack *git.Stack, gr git.GitRunner) (bool, error) {
	fmt.Println(progressString(io, ref.Branch+" has diverged. Rebasing..."))

	err := rebaseWithUpdateRefs(io, ref.Branch, stack, gr)
	if err != nil {
		return false, err
	}

	return true, nil
}

func branchBehind(io *iostreams.IOStreams, ref *git.StackRef, gr git.GitRunner) error {
	// possibly someone applied suggestions or someone else added a
	// different commit
	fmt.Println(progressString(io, ref.Branch+" is behind - pulling updates."))

	_, err := gitPull(gr)
	if err != nil {
		return fmt.Errorf("error checking for a running Git pull: %w", err)
	}

	return nil
}

func populateMR(io *iostreams.IOStreams, ref *git.StackRef, opts *options, client *gitlab.Client, gr git.GitRunner) error {
	// no MR - lets create one!
	fmt.Println(progressString(io, ref.Branch+" needs a merge request. Creating it now."))

	mr, err := createMR(client, opts, ref, gr)
	if err != nil {
		return fmt.Errorf("error updating stack ref files: %w", err)
	}

	fmt.Println(progressString(io, "Merge request created!"))
	fmt.Println(mrutils.DisplayMR(io.Color(), &mr.BasicMergeRequest, true))

	// update the ref
	ref.MR = mr.WebURL
	err = git.UpdateStackRefFile(opts.stack.Title, *ref)
	if err != nil {
		return fmt.Errorf("error updating stack ref files: %w", err)
	}

	return nil
}
