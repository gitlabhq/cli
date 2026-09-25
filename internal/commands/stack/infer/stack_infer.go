package infer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	stackName  string
	baseBranch string
}

func NewCmdInferStack(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	o := &options{}

	stackInferCmd := &cobra.Command{
		Use:   "infer <revision-range>",
		Short: `Add layers to a stack based on a range of commits. (EXPERIMENTAL)`,
		Long: `Add layers to a stack based on a range of commits.
This will append layers to an existing stack, or create a new one if needed.
` + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Commit range syntax is similar to "git rev-list".
			# The start of the range must be a branch name (not a relative ref like HEAD~5).

			# Infer stack from commits between main and current branch
			glab stack infer main..HEAD

			# Infer stack from commits on a feature branch since it diverged from develop
			glab stack infer develop..HEAD

			# Create a new stack with a specific name
			glab stack infer --name feature-stack main..HEAD
		`),
		Args: cobra.MinimumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), f, gr, args, o)
		},
	}

	stackInferCmd.Flags().StringVarP(&o.stackName, "name", "n", "", "Name for the new stack (used when creating a stack)")

	return stackInferCmd
}

func parseBaseBranch(gr git.GitRunner, args []string) (string, error) {
	for _, arg := range args {
		if before, _, found := strings.Cut(arg, ".."); found {
			return resolveBaseBranch(gr, before)
		}
	}
	return "", nil
}

// resolveBaseBranch resolves a revision expression to a branch name.
// Relative refs like HEAD~3 are not valid base branches because they drift
// as new commits are added.
func resolveBaseBranch(gr git.GitRunner, ref string) (string, error) {
	branch, err := gr.Git("rev-parse", "--abbrev-ref", ref)
	if err != nil {
		return "", fmt.Errorf("could not resolve %q to a branch: %w", ref, err)
	}

	branch = strings.TrimSpace(branch)

	// rev-parse --abbrev-ref returns the ref unchanged when it can't
	// abbreviate it to a symbolic name (e.g. HEAD~3 stays HEAD~3).
	// Detect this by checking if the resolved name still matches the
	// original non-branch-like ref.
	if branch == ref && strings.ContainsAny(ref, "~^@{}") {
		return "", fmt.Errorf(
			"%q is a relative revision, not a branch name. "+
				"Use a branch name as the start of the range (e.g. main..HEAD)",
			ref,
		)
	}

	return branch, nil
}

func run(ctx context.Context, f cmdutils.Factory, gr git.GitRunner, args []string, o *options) error {
	baseBranch, err := parseBaseBranch(gr, args)
	if err != nil {
		return err
	}
	o.baseBranch = baseBranch

	// check if in a stack
	title, err := git.GetCurrentStackTitle()
	if err != nil {
		title, err = promptAndCreateStack(ctx, f, gr, o)
		if err != nil {
			return fmt.Errorf("could not create new stack: %w", err)
		}
	}

	stack, err := git.GatherStackRefs(title)
	if err != nil {
		stack = git.Stack{Title: title, Refs: make(map[string]git.StackRef)}
	}

	io := f.IO()
	color := io.Color()

	io.StopSpinner("")
	// pausing the spinner in case it's a terminal based editor

	commits, err := promptForCommits(ctx, f, gr, args)
	if err != nil {
		return fmt.Errorf("error getting commits for stack: %w", err)
	}

	if len(commits) == 0 {
		return fmt.Errorf("no commits selected for stack")
	}

	io.StartSpinner("Creating stack layers...")
	defer io.StopSpinner("")

	err = createBranches(f, gr, commits, title, stack)
	if err != nil {
		return fmt.Errorf("error creating stack layers: %w", err)
	}

	io.StopSpinner("")
	io.LogInfof("%s Added %d layer(s) to stack %q. Run `glab stack sync` to push and create merge requests.\n",
		color.GreenCheck(), len(commits), title)

	return nil
}

func createBranches(f cmdutils.Factory, gr git.GitRunner, commits []string, title string, stack git.Stack) error {
	author, err := git.GitUserName()
	if err != nil {
		return fmt.Errorf("error getting Git author: %w", err)
	}

	originalBranch, err := gr.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return fmt.Errorf("error getting current branch: %w", err)
	}
	originalBranch = strings.TrimSpace(originalBranch)

	restoreOriginalBranch := func() {
		_, _ = gr.Git("checkout", originalBranch)
	}
	defer restoreOriginalBranch()

	baseBranch, err := stack.BaseBranch(gr)
	if err != nil {
		return fmt.Errorf("error getting stack base branch: %w", err)
	}

	var prevSHA string
	prevBranch := baseBranch
	if !stack.Empty() {
		last := stack.Last()
		prevSHA = last.SHA
		prevBranch = last.Branch
	}

	var createdBranches []string
	var createdRefs []git.StackRef
	modifiedRefs := make(map[string]git.StackRef) // original state of existing refs we modified

	rollback := func() {
		restoreOriginalBranch()
		for _, b := range createdBranches {
			_, _ = gr.Git("branch", "-D", b)
		}
		for _, ref := range createdRefs {
			_ = git.DeleteStackRefFile(title, ref)
		}
		for _, originalRef := range modifiedRefs {
			_ = git.UpdateStackRefFile(title, originalRef)
		}
	}

	for i, commitHash := range commits {
		description, err := stackutils.CommitSubject(gr, commitHash)
		if err != nil {
			rollback()
			return fmt.Errorf("error getting commit subject for %s: %w", commitHash, err)
		}

		stackSHA, err := stackutils.GenerateStackSha(description, title, string(author), time.Now())
		if err != nil {
			rollback()
			return fmt.Errorf("error generating stack SHA: %w", err)
		}

		branchName, err := stackutils.CreateShaBranch(f, stackSHA, title)
		if err != nil {
			rollback()
			return fmt.Errorf("error creating branch name: %w", err)
		}

		_, err = gr.Git("checkout", "-b", branchName, prevBranch)
		if err != nil {
			rollback()
			return fmt.Errorf("error creating branch %s from %s: %w", branchName, prevBranch, err)
		}
		createdBranches = append(createdBranches, branchName)

		_, err = gr.Git("cherry-pick", commitHash)
		if err != nil {
			_, _ = gr.Git("cherry-pick", "--abort")
			rollback()
			return fmt.Errorf(
				"conflict cherry-picking commit %d/%d (%s %q) onto %s. "+
					"The selected commits may not be independent — try selecting a contiguous range",
				i+1, len(commits), commitHash, description, prevBranch,
			)
		}

		if prevSHA != "" {
			prevRef := stack.Refs[prevSHA]
			if _, tracked := modifiedRefs[prevSHA]; !tracked {
				modifiedRefs[prevSHA] = prevRef // save original state before mutation
			}
			prevRef.Next = stackSHA
			err = git.UpdateStackRefFile(title, prevRef)
			if err != nil {
				rollback()
				return fmt.Errorf("error updating previous ref: %w", err)
			}
			stack.Refs[prevSHA] = prevRef
		}

		newRef := git.StackRef{
			Prev:        prevSHA,
			SHA:         stackSHA,
			Branch:      branchName,
			Description: description,
		}

		err = git.AddStackRefFile(title, newRef)
		if err != nil {
			rollback()
			return fmt.Errorf("error creating stack ref file: %w", err)
		}

		createdRefs = append(createdRefs, newRef)
		stack.Refs[stackSHA] = newRef
		prevSHA = stackSHA
		prevBranch = branchName
	}

	return nil
}

// promptAndCreateStack creates a new stack with the provided name or prompts for one
func promptAndCreateStack(ctx context.Context, f cmdutils.Factory, gr git.GitRunner, o *options) (string, error) {
	var titleString string

	if o.stackName != "" {
		titleString = o.stackName
	} else {
		if !f.IO().IsOutputTTY() {
			return "", fmt.Errorf("no stack found and no TTY available. Use --name to specify a stack name")
		}
		err := f.IO().Input(ctx, &titleString, "No stack found. Enter a name for a new stack:", "", func(s string) error {
			if s == "" {
				return fmt.Errorf("title is required")
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("error prompting for title: %w", err)
		}
	}

	io := f.IO()
	color := io.Color()

	title := utils.ReplaceNonAlphaNumericChars(titleString, "-")
	if title != titleString {
		io.LogErrorf("%s warning: invalid characters have been replaced with dashes: %s\n",
			color.WarnIcon(),
			color.Blue(title))
	}

	err := git.SetLocalConfig("glab.currentstack", title)
	if err != nil {
		return "", fmt.Errorf("error setting local Git config: %w", err)
	}

	_, err = git.AddStackRefDir(title)
	if err != nil {
		return "", fmt.Errorf("error adding stack metadata directory: %w", err)
	}

	baseBranch := o.baseBranch
	if baseBranch == "" {
		baseBranch, err = gr.Git("symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil {
			return "", fmt.Errorf("error determining current branch: %w", err)
		}
	}

	err = git.AddStackBaseBranch(title, baseBranch)
	if err != nil {
		return "", fmt.Errorf("error adding base branch to metadata: %w", err)
	}

	io.LogInfof("New stack created with title %q.\n", title)

	return title, nil
}
