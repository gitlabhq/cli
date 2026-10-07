package list

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmdStackList(f cmdutils.Factory, gr git.GitRunner) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all diffs in the stack. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Shows the branch and description of each diff. To check out a different diff, use %[1]sglab stack move%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack list`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			title, err := git.GetCurrentStackTitle()
			if err != nil {
				return err
			}

			stack, err := git.GatherStackRefs(title)
			if err != nil {
				return err
			}

			currentBranch, err := git.CurrentBranch()
			if err != nil {
				return err
			}

			run(f.IO(), stack, currentBranch)
			return nil
		},
	}
}

func run(io *iostreams.IOStreams, stack git.Stack, currentBranch string) {
	c := io.Color()
	for ref := range stack.Iter() {
		if currentBranch == ref.Branch {
			io.LogInfof("> %s - %s\n", c.Bold(ref.Branch), c.Cyan(ref.Subject()))
		} else {
			io.LogInfof("  %s - %s\n", ref.Branch, c.Cyan(ref.Subject()))
		}
	}
}
