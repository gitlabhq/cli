package incident

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	incidentCloseCmd "gitlab.com/gitlab-org/cli/internal/commands/incident/close"
	incidentListCmd "gitlab.com/gitlab-org/cli/internal/commands/incident/list"
	incidentNoteCmd "gitlab.com/gitlab-org/cli/internal/commands/incident/note"
	incidentReopenCmd "gitlab.com/gitlab-org/cli/internal/commands/incident/reopen"
	incidentSubscribeCmd "gitlab.com/gitlab-org/cli/internal/commands/incident/subscribe"
	incidentUnsubscribeCmd "gitlab.com/gitlab-org/cli/internal/commands/incident/unsubscribe"
	incidentViewCmd "gitlab.com/gitlab-org/cli/internal/commands/incident/view"
)

func NewCmdIncident(f cmdutils.Factory) *cobra.Command {
	incidentCmd := &cobra.Command{
		Use:   "incident [command] [flags]",
		Short: `Work with GitLab incidents.`,
		Long: heredoc.Docf(`
			Incidents track service disruptions and their resolution. List and
			view incidents, comment on them, and subscribe, close, or reopen
			them.

			Use %[1]s--repo%[1]s to target a project other than the current one.
		`, "`"),
		Example: heredoc.Doc(`
			glab incident list`),
		Annotations: map[string]string{
			"help:arguments": heredoc.Doc(`
				Specify an incident in one of the following formats:
				- By number, for example "123"
				- By URL, for example "https://gitlab.com/NAMESPACE/REPO/-/issues/incident/123"
			`),
		},
	}

	cmdutils.EnableRepoOverride(incidentCmd, f)

	incidentCmd.AddCommand(incidentListCmd.NewCmdList(f, nil))
	incidentCmd.AddCommand(incidentNoteCmd.NewCmdNote(f))
	incidentCmd.AddCommand(incidentViewCmd.NewCmdView(f))
	incidentCmd.AddCommand(incidentCloseCmd.NewCmdClose(f))
	incidentCmd.AddCommand(incidentReopenCmd.NewCmdReopen(f))
	incidentCmd.AddCommand(incidentSubscribeCmd.NewCmdSubscribe(f))
	incidentCmd.AddCommand(incidentUnsubscribeCmd.NewCmdUnsubscribe(f))
	return incidentCmd
}
