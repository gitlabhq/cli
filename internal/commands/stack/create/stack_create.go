package create

import (
	"fmt"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/briandowns/spinner"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func NewCmdCreateStack(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	stackCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new stack. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			The stack starts empty, and the other %[1]sglab stack%[1]s commands act on it until you switch. The branch you have checked out becomes its base branch, which the first merge request targets, so push it to the remote before you run %[1]sglab stack sync%[1]s. To add diffs, use %[1]sglab stack save%[1]s.

			This command adds metadata to your %[1]s./.git/stacked%[1]s directory.
		`, "`") + text.ExperimentalString,
		Aliases: []string{"new"},
		Example: heredoc.Doc(`
			glab stack create cool-new-feature
			glab stack new cool-new-feature`),
		Args: cobra.MaximumNArgs(10),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := stackutils.CheckNoRebaseInProgress(); err != nil {
				return err
			}

			var titleString string

			switch len(args) {
			case 1:
				titleString = args[0]
			case 0:
				err := f.IO().Input(cmd.Context(), &titleString, "New stack title?", "", func(s string) error {
					if s == "" {
						return fmt.Errorf("title is required")
					}
					return nil
				})
				if err != nil {
					return fmt.Errorf("error prompting for title: %w", err)
				}
			default:
				titleString = strings.Join(args, "-")
			}

			s := spinner.New(spinner.CharSets[11], 100*time.Millisecond)
			color := f.IO().Color()

			title := utils.ReplaceNonAlphaNumericChars(titleString, "-")
			if title != titleString {
				f.IO().LogErrorf("%s warning: invalid characters have been replaced with dashes: %s\n",
					color.WarnIcon(),
					color.Blue(title))
			}

			err := git.SetLocalConfig("glab.currentstack", title)
			if err != nil {
				return fmt.Errorf("error setting local Git config: %w", err)
			}

			_, err = git.AddStackRefDir(title)
			if err != nil {
				return fmt.Errorf("error adding stack metadata directory: %w", err)
			}

			currentBranch, err := gr.Git("symbolic-ref", "--quiet", "--short", "HEAD")
			if err != nil {
				return fmt.Errorf("error getting current branch: %w", err)
			}

			err = git.AddStackBaseBranch(title, currentBranch)
			if err != nil {
				return fmt.Errorf("error adding current branch to metadata: %w", err)
			}

			if f.IO().IsOutputTTY() {
				f.IO().LogInfof("New stack created with title \"%s\".\n", title)
			}

			s.Stop()

			return nil
		},
	}
	return stackCreateCmd
}
