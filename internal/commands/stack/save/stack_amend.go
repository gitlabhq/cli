package save

import (
	"context"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/briandowns/spinner"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmdAmendStack(f cmdutils.Factory, gr git.GitRunner, getText cmdutils.GetTextUsingEditor) *cobra.Command {
	var amendStageAll bool
	stackSaveCmd := &cobra.Command{
		Use:   "amend",
		Short: `Save more changes to a stacked diff. (EXPERIMENTAL)`,
		Long: `Add more changes to an existing stacked diff.
` + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Amend diff with currently staged changes
			glab stack amend -m "Fix a function"

			# Add specified file to staged changes and amend diff
			glab stack amend newfile -m "forgot to add this"

			# Add all tracked files to staged changes and amend diff
			glab stack amend -a -m "fixed a function in exisiting file"

			# Add all tracked and untracked files to staged changes and amend diff
			glab stack amend . -m "refactored file into new files"`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			output, err := amendFunc(cmd.Context(), f, args, getText, description, amendStageAll)
			if err != nil {
				return fmt.Errorf("could not run stack amend: %v", err)
			}

			if f.IO().IsOutputTTY() {
				fmt.Fprint(f.IO().StdOut, output)
			}

			return nil
		},
	}
	stackSaveCmd.Flags().StringVarP(&description, "description", "d", "", "a description of the change")
	stackSaveCmd.Flags().StringVarP(&description, "message", "m", "", "alias for the description flag")
	stackSaveCmd.Flags().BoolVarP(&amendStageAll, "all", "a", false, "Automatically stage modified and deleted tracked files (default false)")
	stackSaveCmd.MarkFlagsMutuallyExclusive("message", "description")

	return stackSaveCmd
}

func amendFunc(ctx context.Context, f cmdutils.Factory, args []string, getText cmdutils.GetTextUsingEditor, description string, stageAll bool) (string, error) {
	// check if there are even any changes before we start
	err := checkForChanges()
	if err != nil {
		return "", fmt.Errorf("could not save: %v", err)
	}

	// get stack title
	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return "", fmt.Errorf("error running Git command: %v", err)
	}

	ref, err := git.CurrentStackRefFromCurrentBranch(title)
	if err != nil {
		return "", fmt.Errorf("error checking for stack: %v", err)
	}

	if ref.Branch == "" {
		return "", fmt.Errorf("not currently in a stack. Change to the branch you want to amend.")
	}

	// a description is required, so ask if one is not provided
	if description == "" {
		description, err = promptForCommit(ctx, f, getText, ref.Description)
		if err != nil {
			return "", fmt.Errorf("error getting commit message: %v", err)
		}
	}

	s := spinner.New(spinner.CharSets[11], 100*time.Millisecond)

	// git add files
	err = addFiles(args[0:], stageAll)
	if err != nil {
		return "", fmt.Errorf("error adding files: %v", err)
	}

	// run the amend commit
	err = gitAmend(description)
	if err != nil {
		return "", fmt.Errorf("error amending commit with Git: %v", err)
	}

	var output string
	if f.IO().IsOutputTTY() {
		output = fmt.Sprintf("Amended stack item with description: %q.\n", description)
	}

	s.Stop()

	return output, nil
}

func gitAmend(description string) error {
	amendCmd := git.GitCommand("commit", "--amend", "-m", description)
	output, err := run.PrepareCmd(amendCmd).Output()
	if err != nil {
		return fmt.Errorf("error running Git command: %v", err)
	}

	fmt.Println("Amend commit: ", string(output))

	return nil
}
