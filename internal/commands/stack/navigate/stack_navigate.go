package navigate

import (
	"errors"
	"fmt"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func baseCommand() (git.Stack, error) {
	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return git.Stack{}, err
	}

	stack, err := git.GatherStackRefs(title)
	if err != nil {
		return git.Stack{}, err
	}

	return stack, nil
}

func NewCmdStackFirst(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	return &cobra.Command{
		Use:   "first",
		Short: "Moves to the first diff in the stack. (EXPERIMENTAL)",
		Long:  "Moves to the first diff in the stack, and checks out that branch.\n" + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack first`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stack, err := baseCommand()
			if err != nil {
				return err
			}

			if stack.Empty() {
				return errors.New("you are on an empty stack; to use a stack, first save a diff")
			}

			ref := stack.First()
			err = git.CheckoutBranch(ref.Branch, gr)
			if err != nil {
				return err
			}

			switchMessage(f, &ref)

			return nil
		},
	}
}

func NewCmdStackNext(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	return &cobra.Command{
		Use:   "next",
		Short: "Moves to the next diff in the stack. (EXPERIMENTAL)",
		Long:  "Moves to the next diff in the stack, and checks out that branch.\n" + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack next`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stack, err := baseCommand()
			if err != nil {
				return err
			}

			ref, err := git.CurrentStackRefFromCurrentBranch(stack.Title)
			if err != nil {
				return err
			}

			if ref.IsLast() {
				return errors.New("you are already at the last diff; use `glab stack list` to see the complete list")
			}

			err = git.CheckoutBranch(stack.Refs[ref.Next].Branch, gr)
			if err != nil {
				return err
			}

			next := stack.Refs[ref.Next]
			switchMessage(f, &next)

			return nil
		},
	}
}

func NewCmdStackPrev(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	return &cobra.Command{
		Use:   "prev",
		Short: "Moves to the previous diff in the stack. (EXPERIMENTAL)",
		Long:  "Moves to the previous diff in the stack, and checks out that branch.\n" + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack prev`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stack, err := baseCommand()
			if err != nil {
				return err
			}

			ref, err := git.CurrentStackRefFromCurrentBranch(stack.Title)
			if err != nil {
				return err
			}

			if ref.IsFirst() {
				return errors.New("you are already at the first diff; use `glab stack list` to see the complete list")
			}

			err = git.CheckoutBranch(stack.Refs[ref.Prev].Branch, gr)
			if err != nil {
				return err
			}

			prev := stack.Refs[ref.Prev]
			switchMessage(f, &prev)

			return nil
		},
	}
}

func NewCmdStackLast(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	return &cobra.Command{
		Use:   "last",
		Short: "Moves to the last diff in the stack. (EXPERIMENTAL)",
		Long:  "Moves to the last diff in the stack, and checks out that branch.\n" + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack last`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stack, err := baseCommand()
			if err != nil {
				return err
			}

			if stack.Empty() {
				return errors.New("stack is empty until you save a diff")
			}

			ref := stack.Last()

			err = git.CheckoutBranch(ref.Branch, gr)
			if err != nil {
				return err
			}

			switchMessage(f, &ref)

			return nil
		},
	}
}

func NewCmdStackMove(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	return &cobra.Command{
		Use:   "move",
		Short: "Moves to any selected entry in the stack. (EXPERIMENTAL)",
		Long:  "Shows a menu with a fuzzy finder to select a stack.\n" + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack move`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stack, err := baseCommand()
			if err != nil {
				return err
			}

			options := make([]huh.Option[string], 0)
			i := 1
			for ref := range stack.Iter() {
				label := fmt.Sprintf("%s - %d: %s", ref.Branch, i, ref.Description)
				options = append(options, huh.NewOption(label, ref.Branch))
				i++
			}

			var branch string
			selector := huh.NewSelect[string]().
				Title("Choose a diff to be checked out:").
				Options(options...).
				Value(&branch)

			err = f.IO().Run(cmd.Context(), selector)
			if err != nil {
				return err
			}

			err = git.CheckoutBranch(branch, gr)
			if err != nil {
				return err
			}

			return nil
		},
	}
}

func switchMessage(f cmdutils.Factory, ref *git.StackRef) {
	color := f.IO().Color()
	fmt.Printf(
		"%v Switched to branch: %v - %v\n",
		color.ProgressIcon(),
		color.Blue(ref.Branch),
		color.Bold(ref.Description),
	)
}
