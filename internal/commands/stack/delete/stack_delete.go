package delete

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var errNoStacksFound = errors.New("no stacks found; create one with \"glab stack create\"")

type options struct {
	io *iostreams.IOStreams

	name             string
	skipConfirmation bool
}

func NewCmdDeleteStack(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io: f.IO(),
	}

	stackDeleteCmd := &cobra.Command{
		Use:   "delete [<stack-name>]",
		Short: "Delete a stack. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Removes the stack's local metadata from the %[1]s.git/stacked%[1]s directory.
			Use this command to clean up stacks for merged or abandoned merge requests.
			Branches, commits, and merge requests are not affected.

			If you do not provide a stack name, the command shows a list of stacks for you to choose from.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Interactively pick from the list of available stacks
			glab stack delete

			# Delete a specific stack by name
			glab stack delete <stack-name>

			# Delete a specific stack without the confirmation prompt
			glab stack delete <stack-name> -y`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			if err := opts.validate(); err != nil {
				return err
			}

			if err := opts.run(cmd.Context()); err != nil {
				if errors.Is(err, iostreams.ErrUserCancelled) {
					return err
				}
				return fmt.Errorf("deleting stack failed: %w", err)
			}

			return nil
		},
	}

	stackDeleteCmd.Flags().BoolVarP(&opts.skipConfirmation, "yes", "y", false, "Skip the confirmation prompt.")

	return stackDeleteCmd
}

func (o *options) complete(args []string) {
	if len(args) > 0 {
		o.name = args[0]
	}
}

func (o *options) validate() error {
	if o.io.PromptEnabled() {
		return nil
	}

	if o.name == "" {
		return cmdutils.FlagError{Err: errors.New("the <stack-name> argument is required when prompts are disabled")}
	}

	if !o.skipConfirmation {
		return cmdutils.FlagError{Err: errors.New("--yes or -y flag is required when not running interactively")}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	stacks, err := git.GetStacks()
	if err != nil {
		// The stacked directory does not exist until the first "glab stack create".
		if errors.Is(err, os.ErrNotExist) {
			return errNoStacksFound
		}
		return fmt.Errorf("getting stacks: %w", err)
	}

	if len(stacks) == 0 {
		return errNoStacksFound
	}

	if o.name == "" {
		names := make([]string, 0, len(stacks))
		for _, s := range stacks {
			names = append(names, s.Title)
		}

		if err := o.io.Select(ctx, &o.name, "Choose a stack to delete:", names); err != nil {
			return err
		}
	}

	if !slices.ContainsFunc(stacks, func(s git.Stack) bool { return s.Title == o.name }) {
		return fmt.Errorf("no stack named %q found", o.name)
	}

	if !o.skipConfirmation {
		if err := o.confirmDeletion(ctx); err != nil {
			return err
		}
	}

	// Clear the config before removing the directory so a failure leaves the stack intact.
	// git.Config returns the same error for an unset key and a failed call, so any error means no current stack.
	clearedCurrent := false
	if currentStack, err := git.GetCurrentStackTitle(); err == nil && currentStack == o.name {
		if err := git.UnsetLocalConfig("glab.currentstack"); err != nil {
			return err
		}
		clearedCurrent = true
	}

	if err := git.RemoveStackRefDir(o.name); err != nil {
		if clearedCurrent {
			if restoreErr := git.SetLocalConfig("glab.currentstack", o.name); restoreErr != nil {
				return errors.Join(err, restoreErr)
			}
		}
		return err
	}

	o.io.LogInfof("Deleted stack %s.\n", o.name)

	return nil
}

func (o *options) confirmDeletion(ctx context.Context) error {
	// Corrupted refs should not block deletion, so a failure only skips the count.
	if stack, err := git.GatherStackRefs(o.name); err == nil && !stack.Empty() {
		o.io.LogInfof("Stack %s still has %s.\n", o.name, utils.Pluralize(len(stack.Refs), "diff"))
	}

	o.io.LogInfof("Deleting a stack removes its local metadata. Branches and merge requests are not affected.\n\n")

	var confirmed bool
	if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Delete stack %s?", o.name)); err != nil {
		return cmdutils.WrapError(err, "could not prompt")
	}

	if !confirmed {
		return cmdutils.CancelError()
	}

	return nil
}
