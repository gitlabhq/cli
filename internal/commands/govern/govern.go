package govern

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	governAuditCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/audit"
	governDoctorCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/doctor"
	governSetupCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/setup"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	governCmd := &cobra.Command{
		Use:   "govern <command>",
		Short: "Manage AI agent governance. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Manage AI agent governance for external agents running against GitLab projects. Configure hooks and diagnose setup issues.
		`) + text.ExperimentalString,
	}

	governCmd.AddCommand(governAuditCmd.NewCmd(f))
	governCmd.AddCommand(governDoctorCmd.NewCmd(f))
	governCmd.AddCommand(governSetupCmd.NewCmd(f))

	return governCmd
}
