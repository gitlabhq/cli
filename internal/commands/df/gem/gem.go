package gem

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/df/dfcmd"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return dfcmd.NewProxyCmd(f, dfcmd.ProxySpec{
		Manager: pm.Gem,
		Use:     "gem <gem args>",
		Short:   "Run gem through the GitLab Dependency Firewall.",
		Long: heredoc.Docf(`
			Run the gem binary through the GitLab Dependency Firewall. The command checks each package download and upload against the policy for the current project, refuses blocked packages, and summarizes the results after the run.

			The command uses your configured sources, and does not modify %[1]s~/.gemrc%[1]s or your sources.

			All arguments are forwarded to %[1]sgem%[1]s verbatim.
		`, "`"),
		Example: heredoc.Doc(`
			# Install a package through the Dependency Firewall
			glab dependency-firewall gem install rake
		`),
	})
}
