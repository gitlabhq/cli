package reorder

import (
	"context"
	"errors"
	"fmt"
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
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	labClient *gitlab.Client
	baseRepo  func() (glrepo.Interface, error)
	remotes   func() (glrepo.Remotes, error)
}

func NewCmdReorderStack(f cmdutils.Factory, gr git.GitRunner, getText cmdutils.GetTextUsingEditor) *cobra.Command {
	opts := &options{
		io:       f.IO(),
		remotes:  f.Remotes,
		baseRepo: f.BaseRepo,
	}

	stackSaveCmd := &cobra.Command{
		Use:   "reorder",
		Short: "Reorder a stack of merge requests. (EXPERIMENTAL)",
		Long:  "Reorder how the current stack's merge requests are merged." + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack reorder`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd.Context(), f, getText)
		},
	}
	return stackSaveCmd
}

func (o *options) run(ctx context.Context, f cmdutils.Factory, getText cmdutils.GetTextUsingEditor) error {
	o.io.StartSpinner("Reordering\n")
	defer o.io.StopSpinner("")

	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return fmt.Errorf("error retrieving current stack title: %w", err)
	}

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

	client, err := auth.GetAuthenticatedClient(f.Config(), f.GitLabClient, f.IO())
	if err != nil {
		return fmt.Errorf("error authorizing with GitLab: %w", err)
	}
	o.labClient = client

	err = updateMRs(ctx, f, updatedStack, stack)
	if err != nil {
		return fmt.Errorf("error updating merge requests: %w", err)
	}

	o.io.StopSpinner("%s Reordering complete\n", f.IO().Color().GreenCheck())

	return nil
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

		// update the stack file
		err = git.UpdateStackRefFile(newStack.Title, newRef)
		if err != nil {
			return git.Stack{}, err
		}

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

func updateMRs(ctx context.Context, f cmdutils.Factory, newStack git.Stack, oldStack git.Stack) error {
	for _, ref := range newStack.Iter2() {
		// if there is already an MR and the order has been adjusted
		if ref.MR != "" &&
			(ref.Next != oldStack.Refs[ref.SHA].Next ||
				ref.Prev != oldStack.Refs[ref.SHA].Prev) {

			client, err := f.GitLabClient()
			if err != nil {
				return fmt.Errorf("error connecting to GitLab: %w", err)
			}

			mr, _, err := mrutils.MRFromArgsWithOpts(ctx, f, []string{ref.Branch}, nil, "opened")
			if err != nil {
				return fmt.Errorf("error getting merge request from GitLab: %w", err)
			}

			var previousBranch string

			if ref.Prev == "" {
				baseRepo, err := f.BaseRepo()
				if err != nil {
					return fmt.Errorf("error getting base repo: %w", err)
				}
				project, err := api.GetProject(client, baseRepo.FullName())
				if err != nil {
					return fmt.Errorf("error getting project details: %w", err)
				}
				previousBranch = project.DefaultBranch
			} else {
				previousBranch = newStack.Refs[ref.Prev].Branch
			}

			opts := gitlab.UpdateMergeRequestOptions{TargetBranch: &previousBranch}

			_, err = api.UpdateMR(client, mr.ProjectID, mr.IID, &opts)
			if err != nil {
				return fmt.Errorf("error updating merge request on GitLab: %w", err)
			}
		}
	}

	return nil
}
