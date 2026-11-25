// Package govern holds the AI governance commands of glab minder: setup,
// audit, and doctor. They change other tools' configuration and upload those
// tools' session transcripts, so they keep to these rules:
//
//   - Nothing installs, upgrades, or re-enables them without the user running
//     glab minder setup. No other glab command or upgrade runs setup, and
//     setup refuses to run for a detected coding agent, so that it cannot
//     confirm setup for the user. Detection reads the environment variables
//     agents set, so it misses an agent that sets none or removes them.
//   - Setup never writes trust or approval state into an agent's
//     configuration, such as Codex's hooks.state or trusted_hash.
//   - Hooks and plugins never inject instructions or context into an agent's
//     session. They only start a sync in the background.
package govern

import (
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	governAuditCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/audit"
	governDoctorCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/doctor"
	governSetupCmd "gitlab.com/gitlab-org/cli/internal/commands/govern/setup"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

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
