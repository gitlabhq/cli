package reorder

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/auth"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io *iostreams.IOStreams
	gr git.GitRunner

	continueReorder bool
	abortReorder    bool
}

func NewCmdReorderStack(f cmdutils.Factory, gr git.GitRunner, getText cmdutils.GetTextUsingEditor) *cobra.Command {
	opts := &options{
		io: f.IO(),
		gr: gr,
	}

	stackReorderCmd := &cobra.Command{
		Use:   "reorder",
		Short: "Reorder a stack of diffs. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Opens an editor with one diff per line, so you can rearrange them.

			When you save and close the file, each diff's branch is rebased onto the branch of the diff now before it, and each moved diff's merge request is retargeted to match. The rebased branches are not pushed automatically, so run %[1]sglab stack sync%[1]s to force-push them and replace the old commits on GitLab.

			If a rebase hits a conflict, resolve it, run %[1]sgit rebase --continue%[1]s, and then run %[1]sglab stack reorder --continue%[1]s. To restore the original order instead, run %[1]sglab stack reorder --abort%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Reorder the stack by choosing a new branch order in your editor
			glab stack reorder

			# Continue a reorder after resolving a conflict
			glab stack reorder --continue

			# Abort a reorder and restore the original branch order
			glab stack reorder --abort`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd.Context(), f, getText)
		},
	}

	stackReorderCmd.Flags().BoolVar(&opts.continueReorder, "continue", false,
		"Continue a reorder after resolving conflicts.")
	stackReorderCmd.Flags().BoolVar(&opts.abortReorder, "abort", false,
		"Abort a reorder and restore original branch state.")
	stackReorderCmd.MarkFlagsMutuallyExclusive("continue", "abort")

	return stackReorderCmd
}

func (o *options) run(ctx context.Context, f cmdutils.Factory, getText cmdutils.GetTextUsingEditor) error {
	switch {
	case o.continueReorder:
		return o.runContinue(ctx, f)
	case o.abortReorder:
		return o.runAbort()
	default:
		return o.runReorder(ctx, f, getText)
	}
}

func (o *options) runReorder(ctx context.Context, f cmdutils.Factory, getText cmdutils.GetTextUsingEditor) error {
	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return fmt.Errorf("error retrieving current stack title: %w", err)
	}

	if err := stackutils.CheckNoRebaseInProgress(); err != nil {
		return err
	}

	if err := checkCleanWorktree(o.gr); err != nil {
		return err
	}

	o.io.StartSpinner("Reordering\n")
	defer o.io.StopSpinner("")

	ref, err := git.CurrentStackRefFromCurrentBranch(title)
	if err != nil {
		return fmt.Errorf("error checking for stack: %w", err)
	}

	stack, err := git.GatherStackRefs(title)
	if err != nil {
		return fmt.Errorf("error getting refs from file system: %w", err)
	}

	o.io.StopSpinner("")
	// pausing the spinner in case it's a terminal based editor

	branches, err := promptForOrder(ctx, f, getText, stack, ref.Branch)
	if err != nil {
		return fmt.Errorf("error getting new branch order: %w", err)
	}

	// resuming spinner
	o.io.StartSpinner("Reordering\n")

	updatedStack, err := matchBranchesToStack(stack, branches)
	if err != nil {
		return fmt.Errorf("error matching branches to stack: %w", err)
	}

	if reflect.DeepEqual(stack, updatedStack) {
		return fmt.Errorf("no updates needed")
	}

	state, err := rebaseInNewOrder(o.gr, stack, updatedStack)
	if err != nil {
		return err
	}

	if err := finishReorder(ctx, f, stack.Title, state); err != nil {
		return err
	}

	o.io.StopSpinner("%s Reorder complete. Branches were rebased locally and merge requests retargeted. Run `glab stack sync` to push the rebased branches\n", f.IO().Color().GreenCheck())

	return nil
}

func (o *options) runContinue(ctx context.Context, f cmdutils.Factory) error {
	o.io.StartSpinner("Continuing reorder\n")
	defer o.io.StopSpinner("")

	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return fmt.Errorf("error retrieving current stack title: %w", err)
	}

	state, err := git.ReadReorderState(title)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no reorder in progress for stack %q", title)
		}
		return fmt.Errorf("error reading reorder state: %w", err)
	}

	if state.NextIndex < 0 || state.NextIndex > len(state.NewOrder) {
		return fmt.Errorf(
			"reorder state for stack %q is invalid: index %d is out of range for %d branch(es)",
			title, state.NextIndex, len(state.NewOrder))
	}

	// NextIndex == len(NewOrder) means every rebase finished and only
	// finishReorder is left to retry.
	if state.NextIndex < len(state.NewOrder) {
		if err := o.resumeRebases(title, &state); err != nil {
			return err
		}
	}

	if err := finishReorder(ctx, f, title, state); err != nil {
		return err
	}

	o.io.StopSpinner("%s Reorder complete. Branches were rebased locally and merge requests retargeted. Run `glab stack sync` to push the rebased branches\n", f.IO().Color().GreenCheck())
	return nil
}

// resumeRebases confirms the branch at NextIndex landed on its new parent,
// then rebases the rest of the stack.
func (o *options) resumeRebases(title string, state *git.ReorderState) error {
	resumeBranch := state.NewOrder[state.NextIndex]
	newParentRef := state.BaseBranch
	if state.NextIndex > 0 {
		newParentRef = state.NewOrder[state.NextIndex-1]
	}

	if git.RebaseInProgress() {
		return fmt.Errorf(
			"a git rebase of %q is still in progress.\n"+
				"Resolve any conflicts, mark them resolved with `git add/rm <files>`,\n"+
				"finish it with `git rebase --continue`, then run `glab stack reorder --continue`.\n"+
				"Or abort the reorder with `glab stack reorder --abort`",
			resumeBranch)
	}

	if err := checkCleanWorktree(o.gr); err != nil {
		return err
	}

	rebased, err := branchRebasedOnto(o.gr, resumeBranch, newParentRef, state.OldTips[resumeBranch])
	if err != nil {
		return err
	}
	if !rebased {
		return fmt.Errorf(
			"branch %q is not rebased onto %q; its git rebase was aborted or never finished.\n"+
				"If you were resolving a conflict, mark the files resolved with `git add/rm <files>`,\n"+
				"finish it with `git rebase --continue`, then run `glab stack reorder --continue`.\n"+
				"Otherwise abort the reorder with `glab stack reorder --abort`",
			resumeBranch, newParentRef)
	}

	state.NextIndex++
	if err := git.WriteReorderState(title, *state); err != nil {
		return fmt.Errorf("could not update reorder state: %w", err)
	}

	return executeRebases(o.gr, title, state)
}

func (o *options) runAbort() error {
	o.io.StartSpinner("Aborting reorder\n")
	defer o.io.StopSpinner("")

	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return fmt.Errorf("error retrieving current stack title: %w", err)
	}

	state, err := git.ReadReorderState(title)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no reorder in progress for stack %q", title)
		}
		return fmt.Errorf("error reading reorder state: %w", err)
	}

	if state.NextIndex >= len(state.NewOrder) {
		return fmt.Errorf(
			"cannot abort: every branch in stack %q is already rebased, and some merge requests\n"+
				"may already be retargeted on GitLab, which abort cannot undo.\n"+
				"Run `glab stack reorder --continue` to finish updating the merge requests",
			title)
	}

	_, _ = o.gr.Git("rebase", "--abort")

	// Exits non-zero on a detached HEAD, where no branch is checked out.
	current, _ := o.gr.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	current = strings.TrimSpace(current)

	// git refuses `branch -f` on the checked-out branch, so reset it first:
	// if --keep refuses over uncommitted changes, no other branch has moved yet.
	if oldTip, ok := state.OldTips[current]; ok && current != state.BaseBranch {
		if _, err := o.gr.Git("reset", "--keep", oldTip); err != nil {
			return fmt.Errorf(
				"could not reset %s to %s without losing uncommitted changes: %w\n"+
					"Commit or stash them, then run `glab stack reorder --abort` again",
				current, oldTip, err)
		}
	}

	for _, branch := range slices.Sorted(maps.Keys(state.OldTips)) {
		if branch == state.BaseBranch || branch == current {
			continue
		}
		oldTip := state.OldTips[branch]
		if _, err := o.gr.Git("branch", "-f", branch, oldTip); err != nil {
			return fmt.Errorf("could not reset %s to %s: %w", branch, oldTip, err)
		}
	}

	if _, err := o.gr.Git("checkout", state.OriginalBranch); err != nil {
		return fmt.Errorf("could not restore branch %s: %w", state.OriginalBranch, err)
	}

	if err := git.DeleteReorderState(title); err != nil {
		return fmt.Errorf("could not delete reorder state: %w", err)
	}

	o.io.StopSpinner("%s Reorder aborted\n", o.io.Color().GreenCheck())
	return nil
}

// branchRebasedOnto reports whether branch moved off oldTip and now sits on
// parent. The ancestor check alone is not enough: parent is often already an
// ancestor in the old order (always, for the base branch), so a branch whose
// rebase was aborted would pass it.
func branchRebasedOnto(gr git.GitRunner, branch, parent, oldTip string) (bool, error) {
	tip, err := gr.Git("rev-parse", branch)
	if err != nil {
		return false, fmt.Errorf("could not get tip of %s: %w", branch, err)
	}
	if strings.TrimSpace(tip) == oldTip {
		return false, nil
	}

	// Exits non-zero when parent is not an ancestor of branch.
	_, err = gr.Git("merge-base", "--is-ancestor", parent, branch)
	return err == nil, nil
}

func prepareReorderState(gr git.GitRunner, oldStack, newStack git.Stack) (git.ReorderState, error) {
	currentBranch, err := git.CurrentBranch()
	if err != nil {
		return git.ReorderState{}, fmt.Errorf("could not determine current branch: %w", err)
	}

	baseBranch, err := oldStack.BaseBranch(gr)
	if err != nil {
		return git.ReorderState{}, fmt.Errorf("could not determine base branch: %w", err)
	}

	oldTips := map[string]string{}
	oldParent := map[string]string{}
	for ref := range oldStack.Iter() {
		tip, err := gr.Git("rev-parse", ref.Branch)
		if err != nil {
			return git.ReorderState{}, fmt.Errorf("could not get tip of %s: %w", ref.Branch, err)
		}
		oldTips[ref.Branch] = strings.TrimSpace(tip)

		if ref.Prev == "" {
			oldParent[ref.Branch] = baseBranch
		} else {
			oldParent[ref.Branch] = oldStack.Refs[ref.Prev].Branch
		}
	}
	baseTip, err := gr.Git("rev-parse", baseBranch)
	if err != nil {
		return git.ReorderState{}, fmt.Errorf("could not get tip of base branch %s: %w", baseBranch, err)
	}
	oldTips[baseBranch] = strings.TrimSpace(baseTip)

	state := git.ReorderState{
		NewOrder:       newStack.Branches(),
		NextIndex:      0,
		OldTips:        oldTips,
		OldParent:      oldParent,
		BaseBranch:     baseBranch,
		OriginalBranch: currentBranch,
		OldRefs:        oldStack.Refs,
		NewRefs:        newStack.Refs,
	}

	if err := git.WriteReorderState(oldStack.Title, state); err != nil {
		return git.ReorderState{}, fmt.Errorf("could not write reorder state: %w", err)
	}

	return state, nil
}

func executeRebases(gr git.GitRunner, title string, state *git.ReorderState) error {
	for i := state.NextIndex; i < len(state.NewOrder); i++ {
		branch := state.NewOrder[i]

		var newParentRef string
		if i == 0 {
			newParentRef = state.BaseBranch
		} else {
			newParentRef = state.NewOrder[i-1]
		}

		oldParentTip := state.OldTips[state.OldParent[branch]]

		if _, err := gr.Git("checkout", branch); err != nil {
			return fmt.Errorf("could not checkout %s: %w", branch, err)
		}

		if _, err := gr.Git("rebase", "--onto", newParentRef, oldParentTip); err != nil {
			// A conflict leaves the rebase stopped partway; any other failure
			// means git refused to start it.
			stopErr := fmt.Errorf(
				"could not rebase %s onto %s: %w\n"+
					"Run `glab stack reorder --abort` to restore the original branches",
				branch, newParentRef, err)
			if git.RebaseInProgress() {
				stopErr = fmt.Errorf(
					"rebase conflict on %s onto %s.\n\n"+
						"To continue:\n"+
						"  1. Edit the conflicted files to resolve the conflicts\n"+
						"     (`git status` shows which files)\n"+
						"  2. Mark them resolved:    git add/rm <files>\n"+
						"  3. Finish the git rebase: git rebase --continue\n"+
						"  4. Resume the reorder:    glab stack reorder --continue\n\n"+
						"Or abort the reorder with:\n"+
						"  glab stack reorder --abort",
					branch, newParentRef)
			}

			// Persist progress so --continue or --abort can pick up from this branch.
			state.NextIndex = i
			if werr := git.WriteReorderState(title, *state); werr != nil {
				return errors.Join(stopErr, fmt.Errorf(
					"additionally, the reorder state could not be saved, so "+
						"`glab stack reorder --continue` may not resume correctly; "+
						"consider `glab stack reorder --abort`: %w", werr))
			}
			return stopErr
		}

		state.NextIndex = i + 1
		if err := git.WriteReorderState(title, *state); err != nil {
			return fmt.Errorf("could not update reorder state: %w", err)
		}
	}

	if _, err := gr.Git("checkout", state.OriginalBranch); err != nil {
		return fmt.Errorf("could not restore branch %s: %w", state.OriginalBranch, err)
	}

	return nil
}

func rebaseInNewOrder(gr git.GitRunner, oldStack, newStack git.Stack) (git.ReorderState, error) {
	state, err := prepareReorderState(gr, oldStack, newStack)
	if err != nil {
		return git.ReorderState{}, err
	}

	if err := executeRebases(gr, oldStack.Title, &state); err != nil {
		return git.ReorderState{}, err
	}

	return state, nil
}

// finishReorder runs after every branch is rebased. REORDER_STATE is deleted
// only once it succeeds, so any failure here can be retried with --continue.
func finishReorder(ctx context.Context, f cmdutils.Factory, title string, state git.ReorderState) error {
	for _, ref := range state.NewRefs {
		if err := git.UpdateStackRefFile(title, ref); err != nil {
			return fmt.Errorf(
				"rebasing is done, but the stack ref file for %s could not be updated: %w\n"+
					"Run `glab stack reorder --continue` to retry",
				ref.Branch, err)
		}
	}

	oldStack := git.Stack{Title: title, Refs: state.OldRefs}
	newStack := git.Stack{Title: title, Refs: state.NewRefs}
	if err := updateMRsForReorder(ctx, f, state.BaseBranch, newStack, oldStack); err != nil {
		return fmt.Errorf(
			"rebasing is done, but updating merge requests failed: %w\n"+
				"Run `glab stack reorder --continue` to retry the merge request update",
			err)
	}

	return clearReorderState(title)
}

// checkCleanWorktree ignores untracked files: git rebases over them unless one
// would be overwritten, and executeRebases reports that failure itself.
func checkCleanWorktree(gr git.GitRunner) error {
	status, err := gr.Git("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Errorf("could not check for uncommitted changes: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("you have uncommitted changes. Commit or stash them, then run the command again")
	}
	return nil
}

func clearReorderState(title string) error {
	if err := git.DeleteReorderState(title); err != nil {
		return fmt.Errorf(
			"reorder finished but its state file could not be removed: %w\n"+
				"Run `glab stack reorder --continue` to retry, otherwise other stack commands will report a reorder in progress",
			err)
	}
	return nil
}

// updateMRsForReorder authenticates, then retargets each reordered merge
// request onto the branch that now precedes it.
func updateMRsForReorder(ctx context.Context, f cmdutils.Factory, baseBranch string, newStack, oldStack git.Stack) error {
	client, err := auth.GetAuthenticatedClient(f.Config(), f.GitLabClient, f.IO())
	if err != nil {
		return fmt.Errorf("error authorizing with GitLab: %w", err)
	}

	return updateMRs(ctx, f, client, baseBranch, newStack, oldStack)
}

func matchBranchesToStack(stack git.Stack, branches []string) (git.Stack, error) {
	stackBranches := stack.Branches()

	// need to clone the refs here so we don't modify the original stack
	newStack := git.Stack{Title: stack.Title, Refs: make(map[string]git.StackRef)}

	for index, branch := range branches {
		// let's find a ref from the branch
		ref, err := stack.RefFromBranch(branch)
		if err != nil {
			return git.Stack{}, fmt.Errorf("could not match branch to stack ref: %w", err)
		}

		var next string
		var prev string

		if index == 0 {
			// first branch in the stack
			prev = ""
		} else {
			// otherwise, get the previous ref
			prevRef, err := stack.RefFromBranch(branches[index-1])
			if err != nil {
				return git.Stack{}, err
			}

			prev = prevRef.SHA
		}

		if index == len(branches)-1 {
			// last branch in the stack
			next = ""
		} else {
			// otherwise, get the next ref
			nextRef, err := stack.RefFromBranch(branches[index+1])
			if err != nil {
				return git.Stack{}, err
			}

			next = nextRef.SHA
		}

		newRef := ref
		newRef.Next = next
		newRef.Prev = prev

		newStack.Refs[newRef.SHA] = newRef

		// and remove the branch from our list
		stackBranches = slices.DeleteFunc(stackBranches,
			func(branch string) bool {
				return branch == ref.Branch
			})
	}

	if len(stackBranches) > 0 {
		return git.Stack{},
			errors.New("missing one or more refs from the reordered list: " +
				strings.Join(stackBranches, ", "))
	}

	return newStack, nil
}

func updateMRs(ctx context.Context, f cmdutils.Factory, client *gitlab.Client, baseBranch string, newStack git.Stack, oldStack git.Stack) error {
	for _, ref := range newStack.Iter2() {
		if ref.MR == "" ||
			(ref.Next == oldStack.Refs[ref.SHA].Next &&
				ref.Prev == oldStack.Refs[ref.SHA].Prev) {
			continue
		}

		mr, _, err := mrutils.MRFromArgsWithOpts(ctx, f, []string{ref.Branch}, nil, "opened")
		if err != nil {
			return fmt.Errorf("error getting merge request from GitLab: %w", err)
		}

		previousBranch := baseBranch
		if ref.Prev != "" {
			previousBranch = newStack.Refs[ref.Prev].Branch
		}

		opts := gitlab.UpdateMergeRequestOptions{TargetBranch: &previousBranch}

		_, err = api.UpdateMR(client, mr.ProjectID, mr.IID, &opts)
		if err != nil {
			return fmt.Errorf("error updating merge request on GitLab: %w", err)
		}
	}

	return nil
}
