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
	var noVerify bool
	var reword bool
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
			glab stack amend . -m "refactored file into new files"

			# Reword the commit message without adding any files
			glab stack amend --reword -m "updated commit message"`),
		Args: cobra.ArbitraryArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			output, err := amendFunc(cmd.Context(), f, args, getText, description, amendStageAll, noVerify, reword)
			if err != nil {
				return fmt.Errorf("could not run stack amend: %w", err)
			}

			if f.IO().IsOutputTTY() {
				f.IO().LogInfof("%s", output)
			}

			return nil
		},
	}
	stackSaveCmd.Flags().StringVarP(&description, "description", "d", "", "A description of the change.")
	stackSaveCmd.Flags().StringVarP(&description, "message", "m", "", "Alias for the description flag.")
	stackSaveCmd.Flags().BoolVarP(&amendStageAll, "all", "a", false, "Automatically stage modified and deleted tracked files.")
	stackSaveCmd.Flags().BoolVar(&noVerify, "no-verify", false, "Bypass the pre-commit and commit-msg hooks of git-commit(1).")
	stackSaveCmd.Flags().BoolVar(&reword, "reword", false, "Only update the commit message without staging any files.")
	stackSaveCmd.MarkFlagsMutuallyExclusive("message", "description")
	stackSaveCmd.MarkFlagsMutuallyExclusive("all", "reword")

	return stackSaveCmd
}

func amendFunc(ctx context.Context, f cmdutils.Factory, args []string, getText cmdutils.GetTextUsingEditor, description string, stageAll, noVerify, reword bool) (string, error) {
	if reword && len(args) > 0 {
		return "", fmt.Errorf("--reword cannot be used with file arguments")
	}

	if !reword {
		// check if there are even any changes before we start
		err := checkForChanges()
		if err != nil {
			return "", fmt.Errorf("could not save: %w", err)
		}
	}

	// get stack title
	title, err := git.GetCurrentStackTitle()
	if err != nil {
		return "", fmt.Errorf("error running Git command: %w", err)
	}

	ref, err := git.CurrentStackRefFromCurrentBranch(title)
	if err != nil {
		return "", fmt.Errorf("error checking for stack: %w", err)
	}

	if ref.Branch == "" {
		return "", fmt.Errorf("not currently in a stack; change to the branch you want to amend")
	}

	// a description is required, so ask if one is not provided
	if description == "" {
		description, err = promptForCommit(ctx, f, getText, ref.Description)
		if err != nil {
			return "", fmt.Errorf("error getting commit message: %w", err)
		}
	}

	s := spinner.New(spinner.CharSets[11], 100*time.Millisecond)

	if !reword {
		// git add files
		err = addFiles(args[0:], stageAll)
		if err != nil {
			return "", fmt.Errorf("error adding files: %w", err)
		}
	}

	// run the amend commit
	err = gitAmend(description, noVerify, reword)
	if err != nil {
		return "", fmt.Errorf("error amending commit with Git: %w", err)
	}

	// update the stack ref description to match the new commit message
	ref.Description = description
	err = git.UpdateStackRefFile(title, ref)
	if err != nil {
		return "", fmt.Errorf("error updating stack ref: %w", err)
	}

	var output string
	if f.IO().IsOutputTTY() {
		output = fmt.Sprintf("Amended stack item with description: %q.\n", description)
	}

	s.Stop()

	return output, nil
}

func gitAmend(description string, noVerify, reword bool) error {
	args := []string{"commit", "--amend", "-m", description}
	if noVerify {
		args = append(args, "--no-verify")
	}
	if reword {
		// --only rewords the commit message without committing any
		// changes that may already be staged in the index.
		args = append(args, "--only")
	}
	amendCmd := git.GitCommand(args...)
	output, err := run.PrepareCmd(amendCmd).Output()
	if err != nil {
		return fmt.Errorf("error running Git command: %w", err)
	}

	fmt.Println("Amend commit: ", string(output))

	return nil
}
