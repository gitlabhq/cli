package gradle

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/df/dfcmd"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return dfcmd.NewProxyCmd(f, dfcmd.ProxySpec{
		Manager: pm.Gradle,
		Use:     "gradle <gradle args>",
		Short:   "Run Gradle through the GitLab Dependency Firewall.",
		Long: heredoc.Docf(`
			Run the Gradle binary through the GitLab Dependency Firewall. The command checks each package download and upload against the policy for the current project, refuses blocked packages, and summarizes the results after the run.

			The command uses your package manager's registry or index configuration, and does not modify it.

			The command runs %[1]sgradle%[1]s from your %[1]sPATH%[1]s. The Gradle Wrapper (%[1]s./gradlew%[1]s) is not supported.

			Gradle does not download artifacts already cached in %[1]s~/.gradle/caches/%[1]s, so the firewall does not check them. Clear the cache to check them again.

			All arguments are forwarded to %[1]sgradle%[1]s verbatim.
		`, "`"),
		Example: heredoc.Doc(`
			# Build a project through the Dependency Firewall
			glab dependency-firewall gradle build

			# List dependencies through the Dependency Firewall
			glab dependency-firewall gradle dependencies
		`),
	})
}
