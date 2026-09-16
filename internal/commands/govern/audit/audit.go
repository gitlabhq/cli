package audit

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	auditSyncCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/audit/sync"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	auditCmd := &cobra.Command{
		Use:   "audit <command>",
		Short: "Manage AI agent audit events and sessions. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Manage audit events and sessions for external AI agents.
			Sync session data from local agent transcripts to GitLab.
		`) + text.ExperimentalString,
	}

	auditCmd.AddCommand(auditSyncCmd.NewCmd(f))

	return auditCmd
}
