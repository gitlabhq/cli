package issue

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	issueBoardCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/board"
	issueCloseCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/close"
	issueCreateCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/create"
	issueDeleteCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/delete"
	issueListCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/list"
	issueNoteCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/note"
	issueReopenCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/reopen"
	issueSubscribeCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/subscribe"
	issueUnsubscribeCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/unsubscribe"
	issueUpdateCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/update"
	issueViewCmd "gitlab.com/gitlab-org/cli/internal/commands/issue/view"
)

func NewCmdIssue(f cmdutils.Factory) *cobra.Command {
	issueCmd := &cobra.Command{
		Use:   "issue [command] [flags]",
		Short: `Work with GitLab issues.`,
		Long: heredoc.Docf(`
			Open issues, list and view them, and manage their lifecycle: assign,
			label, comment, close, reopen, and more. Work with issue boards under
			%[1]sissue board%[1]s.

			Use %[1]s--repo%[1]s to target a project other than the current one.
		`, "`"),
		Example: heredoc.Doc(`
			glab issue list
			glab issue create --label --confidential
			glab issue view --web 123
			glab issue note -m "closing because !123 was merged" <issue number>`),
		Annotations: map[string]string{
			"help:arguments": heredoc.Doc(`
				Specify an issue in one of the following formats:
				- By number, for example "123"
				- By URL, for example "https://gitlab.com/NAMESPACE/REPO/-/issues/123"
			`),
		},
	}

	cmdutils.EnableRepoOverride(issueCmd, f)

	issueCmd.AddCommand(issueCloseCmd.NewCmdClose(f))
	issueCmd.AddCommand(issueBoardCmd.NewCmdBoard(f))
	issueCmd.AddCommand(issueCreateCmd.NewCmdCreate(f))
	issueCmd.AddCommand(issueDeleteCmd.NewCmdDelete(f))
	issueCmd.AddCommand(issueListCmd.NewCmdList(f, nil))
	issueCmd.AddCommand(issueNoteCmd.NewCmdNote(f))
	issueCmd.AddCommand(issueReopenCmd.NewCmdReopen(f))
	issueCmd.AddCommand(issueViewCmd.NewCmdView(f))
	issueCmd.AddCommand(issueSubscribeCmd.NewCmdSubscribe(f))
	issueCmd.AddCommand(issueUnsubscribeCmd.NewCmdUnsubscribe(f))
	issueCmd.AddCommand(issueUpdateCmd.NewCmdUpdate(f))
	return issueCmd
}
