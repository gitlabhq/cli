package twine

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/df/dfcmd"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return dfcmd.NewProxyCmd(f, dfcmd.ProxySpec{
		Manager: pm.Twine,
		Use:     "twine <twine args>",
		Short:   "Run Twine through the GitLab Dependency Firewall.",
		Long: heredoc.Docf(`
			Run the Twine binary through the GitLab Dependency Firewall. The command checks each package download and upload against the policy for the current project, refuses blocked packages, and summarizes the results after the run.

			The command uses your package manager's registry or index configuration, and does not modify it.

			All arguments are forwarded to %[1]stwine%[1]s verbatim.
		`, "`"),
		Example: heredoc.Doc(`
			# Upload a package through the Dependency Firewall
			glab dependency-firewall twine upload dist/*
		`),
	})
}
