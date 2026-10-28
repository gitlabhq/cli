package yarn

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/df/dfcmd"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return dfcmd.NewProxyCmd(f, dfcmd.ProxySpec{
		Manager: pm.Yarn,
		Use:     "yarn <yarn args>",
		Short:   "Run Yarn through the GitLab Dependency Firewall.",
		Long: heredoc.Docf(`
			Run the yarn binary through the GitLab Dependency Firewall. The command checks each package download and upload against the policy for the current project, refuses blocked packages, and summarizes the results after the run.

			The command uses your package manager's registry or index configuration, and does not modify it.

			This command requires Yarn 3.2 or later, or Yarn Classic (1.x). It refuses Yarn 2.x through 3.1, because those versions cannot stop a %[1]s.yarnrc.yml%[1]s file from routing registry traffic around the firewall. During the run, Yarn ignores per-host %[1]snetworkSettings%[1]s from every %[1]s.yarnrc.yml%[1]s file, so every request goes through the firewall.

			All arguments are forwarded to %[1]syarn%[1]s verbatim.
		`, "`"),
		Example: heredoc.Doc(`
			# Install dependencies through the Dependency Firewall
			glab dependency-firewall yarn install

			# Add a package through the Dependency Firewall
			glab dependency-firewall yarn add left-pad
		`),
	})
}
