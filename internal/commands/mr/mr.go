package mr

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	mrApproveCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/approve"
	mrApproversCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/approvers"
	mrCheckoutCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/checkout"
	mrCloseCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/close"
	mrCreateCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/create"
	mrDeleteCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/delete"
	mrDiffCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/diff"
	mrForCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/for"
	mrIssuesCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/issues"
	mrListCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/list"
	mrMergeCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/merge"
	mrNoteCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/note"
	mrRebaseCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/rebase"
	mrReopenCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/reopen"
	mrRevokeCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/revoke"
	mrSubscribeCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/subscribe"
	mrTodoCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/todo"
	mrUnsubscribeCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/unsubscribe"
	mrUpdateCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/update"
	mrViewCmd "gitlab.com/gitlab-org/cli/internal/commands/mr/view"
)

func NewCmdMR(f cmdutils.Factory) *cobra.Command {
	mrCmd := &cobra.Command{
		Use:   "mr <command> [flags]",
		Short: `Create, view, and manage merge requests.`,
		Long: heredoc.Docf(`
			Open a merge request from your current branch, list and view existing
			merge requests, and manage them: approve, merge, rebase, comment, and
			more.

			Use %[1]s--repo%[1]s to target a project other than the current one.
		`, "`"),
		Example: heredoc.Doc(`
			glab mr create --fill --label bugfix
			glab mr merge 123
			glab mr note -m "needs to do X before it can be merged" branch-foo`),
		Annotations: map[string]string{
			"help:arguments": heredoc.Doc(`
			Specify a merge request in one of the following formats:
			- By number, for example "123"
			- By the name of its source branch, for example "patch-1" or "OWNER:patch-1"
			- By URL, for example "https://gitlab.com/gitlab-org/cli/-/merge_requests/123"
			`),
		},
	}

	cmdutils.EnableRepoOverride(mrCmd, f)

	mrCmd.AddCommand(mrApproveCmd.NewCmdApprove(f))
	mrCmd.AddCommand(mrApproversCmd.NewCmdApprovers(f))
	mrCmd.AddCommand(mrCheckoutCmd.NewCmdCheckout(f))
	mrCmd.AddCommand(mrCloseCmd.NewCmdClose(f))
	mrCmd.AddCommand(mrCreateCmd.NewCmdCreate(f))
	mrCmd.AddCommand(mrDeleteCmd.NewCmdDelete(f))
	mrCmd.AddCommand(mrDiffCmd.NewCmdDiff(f, nil))
	mrCmd.AddCommand(mrForCmd.NewCmdFor(f))
	mrCmd.AddCommand(mrIssuesCmd.NewCmdIssues(f))
	mrCmd.AddCommand(mrListCmd.NewCmdList(f, nil))
	mrCmd.AddCommand(mrMergeCmd.NewCmdMerge(f))
	mrCmd.AddCommand(mrNoteCmd.NewCmdNote(f))
	mrCmd.AddCommand(mrRebaseCmd.NewCmdRebase(f))
	mrCmd.AddCommand(mrReopenCmd.NewCmdReopen(f))
	mrCmd.AddCommand(mrRevokeCmd.NewCmdRevoke(f))
	mrCmd.AddCommand(mrSubscribeCmd.NewCmdSubscribe(f))
	mrCmd.AddCommand(mrUnsubscribeCmd.NewCmdUnsubscribe(f))
	mrCmd.AddCommand(mrTodoCmd.NewCmdTodo(f))
	mrCmd.AddCommand(mrUpdateCmd.NewCmdUpdate(f))
	mrCmd.AddCommand(mrViewCmd.NewCmdView(f))

	return mrCmd
}
