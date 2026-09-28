package stackswitch

import (
	"errors"
	"fmt"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmdStackSwitch(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	stackSwitchCmd := &cobra.Command{
		Use:   "switch [stack-name]",
		Short: "Switch between stacks. (EXPERIMENTAL)",
		Long: heredoc.Doc(
			"Switch between stacks to work on another stack created with \"glab stack create\".\n" +
				"When stack-name is omitted, choose from the list of all stacks.\n" +
				text.ExperimentalString,
		),
		Example: heredoc.Doc(`
			# Interactively pick from the list of available stacks.
			glab stack switch

			# Switch to a specific stack by name.
			glab stack switch <stack-name>`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := stackutils.CheckNoRebaseInProgress(); err != nil {
				return err
			}
			if err := switchFunc(cmd, f, args); err != nil {
				return fmt.Errorf("switching stacks failed: %w", err)
			}
			return nil
		},
		Args: cobra.MaximumNArgs(1),
	}
	return stackSwitchCmd
}

func switchFunc(cmd *cobra.Command, f cmdutils.Factory, args []string) error {
	errNoStacksFound := errors.New("no stacks found; create one with \"glab stack create\"")

	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" && !f.IO().PromptEnabled() {
		return cmdutils.FlagError{Err: errors.New("the <stack-name> argument is required when prompts are disabled")}
	}

	stacks, err := git.GetStacks()
	if err != nil {
		if name == "" && errors.Is(err, os.ErrNotExist) {
			return errNoStacksFound
		}
		return fmt.Errorf("getting stacks: %w", err)
	}

	if name == "" {
		if len(stacks) == 0 {
			return errNoStacksFound
		}

		options := make([]string, 0, len(stacks))
		for _, s := range stacks {
			options = append(options, s.Title)
		}
		if err := f.IO().Select(cmd.Context(), &name, "Choose a stack to switch to:", options); err != nil {
			return err
		}
	}

	var foundStack *git.Stack
	for _, s := range stacks {
		if s.Title == name {
			foundStack = &s
			break
		}
	}
	if foundStack == nil {
		return fmt.Errorf("no stack named %q found", name)
	}

	currentStackTitle, err := git.GetCurrentStackTitle()
	if err != nil {
		return fmt.Errorf("error getting current stack: %w", err)
	}

	if currentStackTitle == name {
		// No need to switch, we're already on the right stack
		return nil
	}

	err = git.SetLocalConfig("glab.currentstack", name)
	if err != nil {
		return fmt.Errorf("error setting local Git config: %w", err)
	}

	f.IO().LogInfof("Switched to stack %s.\n", name)
	return nil
}
