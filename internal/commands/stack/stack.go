package stack

import (
	"context"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	stackCreateCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/create"
	stackDeleteCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/delete"
	stackInferCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/infer"
	stackListCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/list"
	stackMoveCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/navigate"
	stackReorderCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/reorder"
	stackSaveCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/save"
	stackSwitchCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/switch"
	stackSyncCmd "gitlab.com/gitlab-org/cli/internal/commands/stack/sync"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func wrappedEdit(f cmdutils.Factory) cmdutils.GetTextUsingEditor {
	return func(ctx context.Context, editor, tmpFileName, content string) (string, error) {
		var result string = content
		err := f.IO().Editor(ctx, &result, "Edit", "", content, editor)
		return result, err
	}
}

func NewCmdStack(f cmdutils.Factory) *cobra.Command {
	stackCmd := &cobra.Command{
		Use:   "stack <command> [flags]",
		Short: `Create, manage, and work with stacked diffs. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			A stack is a series of small, dependent merge requests that together deliver a feature. Reviewers can review and merge earlier changes while you keep building on top of them.

			Locally, each diff in the stack is one commit on its own branch, built on the branch of the previous diff. When you run %[1]sglab stack sync%[1]s, each diff becomes a merge request that targets the branch of the previous diff. The first diff targets the base branch.

			The %[1]sglab stack%[1]s commands act on the stack you last created or switched to, regardless of which branch you have checked out.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab stack create cool-new-feature
			glab stack sync`),
		Aliases: []string{"stacks"},
	}

	var gr git.StandardGitCommand

	cmdutils.EnableRepoOverride(stackCmd, f)
	getTextFromEditor := wrappedEdit(f)

	stackCmd.AddCommand(stackCreateCmd.NewCmdCreateStack(f, gr))
	stackCmd.AddCommand(stackSaveCmd.NewCmdSaveStack(f, gr, getTextFromEditor))
	stackCmd.AddCommand(stackSaveCmd.NewCmdAmendStack(f, gr, getTextFromEditor))
	stackCmd.AddCommand(stackSyncCmd.NewCmdSyncStack(f, gr))
	stackCmd.AddCommand(stackMoveCmd.NewCmdStackPrev(f, gr))
	stackCmd.AddCommand(stackMoveCmd.NewCmdStackNext(f, gr))
	stackCmd.AddCommand(stackMoveCmd.NewCmdStackFirst(f, gr))
	stackCmd.AddCommand(stackMoveCmd.NewCmdStackLast(f, gr))
	stackCmd.AddCommand(stackMoveCmd.NewCmdStackMove(f, gr))
	stackCmd.AddCommand(stackListCmd.NewCmdStackList(f, gr))
	stackCmd.AddCommand(stackReorderCmd.NewCmdReorderStack(f, gr, getTextFromEditor))
	stackCmd.AddCommand(stackSwitchCmd.NewCmdStackSwitch(f, gr))
	stackCmd.AddCommand(stackInferCmd.NewCmdInferStack(f, gr))
	stackCmd.AddCommand(stackDeleteCmd.NewCmdDeleteStack(f))

	return stackCmd
}
