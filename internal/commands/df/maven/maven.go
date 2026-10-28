package maven

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/df/dfcmd"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return dfcmd.NewProxyCmd(f, dfcmd.ProxySpec{
		Manager: pm.Maven,
		Use:     "maven <mvn args>",
		Short:   "Run Maven through the GitLab Dependency Firewall.",
		Long: heredoc.Docf(`
			Run the Maven binary (%[1]smvn%[1]s) through the GitLab Dependency Firewall. The command checks each package download and upload against the policy for the current project, refuses blocked packages, and summarizes the results after the run.

			The command uses your package manager's registry or index configuration, and does not modify it.

			The command checks %[1]s.jar%[1]s, %[1]s.pom%[1]s, %[1]s.aar%[1]s, and %[1]s.war%[1]s files, including timestamped snapshot files. It does not check checksum and signature files (%[1]s.md5%[1]s, %[1]s.sha1%[1]s, %[1]s.sha256%[1]s, %[1]s.sha512%[1]s, and %[1]s.asc%[1]s).

			The command runs %[1]smvn%[1]s from your %[1]sPATH%[1]s. The Maven Wrapper (%[1]s./mvnw%[1]s) is not supported.

			Maven does not download artifacts already cached in %[1]s~/.m2/repository/%[1]s, so the firewall does not check them. Clear the cache to check them again.

			All arguments are forwarded to %[1]smvn%[1]s verbatim.
		`, "`"),
		Example: heredoc.Doc(`
			# Build a project through the Dependency Firewall
			glab dependency-firewall maven verify

			# Resolve dependencies through the Dependency Firewall
			glab dependency-firewall maven dependency:resolve

			# Deploy a project through the Dependency Firewall
			glab dependency-firewall maven deploy
		`),
	})
}
