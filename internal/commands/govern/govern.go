package govern

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	governAuditCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/audit"
	governDoctorCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/doctor"
	governSetupCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/setup"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// NewCmd returns the glab minder command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	minderCmd := &cobra.Command{
		Use:   "minder <command>",
		Short: "Manage AI governance. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Manage AI governance for external agents running against GitLab projects. Configure hooks and diagnose setup issues.
		`) + text.ExperimentalString,
	}
	AddSubcommands(minderCmd, f)
	return minderCmd
}

// AddSubcommands adds the audit, doctor and setup subcommands of glab minder
// to cmd.
func AddSubcommands(cmd *cobra.Command, f cmdutils.Factory) {
	cmd.AddCommand(governAuditCmd.NewCmd(f))
	cmd.AddCommand(governDoctorCmd.NewCmd(f))
	cmd.AddCommand(governSetupCmd.NewCmd(f))
}

// NewLegacyCmd returns glab govern, the name glab minder had before. It is
// hidden, and kept so that the hooks, OpenCode plugin and fallback periodic
// sync that earlier glab versions installed keep working. The MCP server
// skips it, so that it does not list each tool twice.
func NewLegacyCmd(f cmdutils.Factory) *cobra.Command {
	governCmd := &cobra.Command{
		Use:    "govern <command>",
		Short:  "Manage AI governance. (EXPERIMENTAL)",
		Hidden: true,
	}
	AddSubcommands(governCmd, f)
	excludeFromMCP(governCmd)
	return governCmd
}

// excludeFromMCP marks cmd and every command under it, because the MCP server
// reads the annotation on each command, not on its parents.
func excludeFromMCP(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[mcpannotations.Exclude] = "true"
	for _, sub := range cmd.Commands() {
		excludeFromMCP(sub)
	}
}
