package npm

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/df/dfcmd"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return dfcmd.NewProxyCmd(f, dfcmd.ProxySpec{
		Manager: pm.NPM,
		Use:     "npm <npm args>",
		Short:   "Run npm through the GitLab Dependency Firewall.",
		Long: heredoc.Doc(`
			Run the npm binary through the GitLab Dependency Firewall. The command checks each package download and upload against the policy for the current project, refuses blocked packages, and summarizes the results after the run.

			The command uses your package manager's registry or index configuration, and does not modify it.

			All arguments are forwarded to npm verbatim.
		`),
		Example: heredoc.Doc(`
			# Install a package through the Dependency Firewall
			glab dependency-firewall npm install left-pad
		`),
	})
}
