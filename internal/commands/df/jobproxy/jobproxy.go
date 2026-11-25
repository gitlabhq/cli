// Package jobproxy implements `glab dependency-firewall job-proxy`: a job-level
// Dependency Firewall proxy that one CI/CD job starts once and routes all of its
// package managers through, instead of wrapping each package-manager command.
package jobproxy

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cmdServe "gitlab.com/gitlab-org/cli/internal/commands/df/jobproxy/serve"
	cmdStart "gitlab.com/gitlab-org/cli/internal/commands/df/jobproxy/start"
	cmdStop "gitlab.com/gitlab-org/cli/internal/commands/df/jobproxy/stop"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "job-proxy <command>",
		Short:   "Run one Dependency Firewall proxy for a whole CI/CD job. (EXPERIMENTAL)",
		Aliases: []string{"session"},
		Long: heredoc.Docf(`
			Run a single Dependency Firewall inspection proxy for the length of a CI/CD job and route every tool in the job through it, instead of wrapping each package-manager command with %[1]sglab dependency-firewall npm%[1]s and the like.

			Start it once at the beginning of the job, in %[1]sbefore_script%[1]s, and evaluate its output with %[1]seval%[1]s. The output sets the environment variables that route the job's package managers through the proxy and make them trust its certificate. The command requires %[1]sgit%[1]s in the job image, unless you set %[1]sGITLAB_REPO%[1]s to %[1]s$CI_PROJECT_PATH%[1]s.

			The proxy checks each package the job downloads or publishes against the policy for the current project, refuses blocked packages, and records blocked and flagged packages in %[1]s.gitlab/df/ci-log.json%[1]s. Run %[1]sglab dependency-firewall ci-summary%[1]s as the last command in %[1]sscript%[1]s, not in %[1]safter_script%[1]s, to fail the job when a package was blocked. GitLab ignores the exit code of %[1]safter_script%[1]s.

			Unlike the per-command wrappers, the job-level proxy also covers tools that are not package managers (for example a raw %[1]scurl%[1]s of a tarball), because the whole job's HTTP and HTTPS traffic is routed through it. Traffic that isn't a package download, such as a call to a service container, is forwarded unchanged.

			The command preserves your existing configuration. It keeps your %[1]sNO_PROXY%[1]s and, when a corporate egress proxy is configured through %[1]sHTTPS_PROXY%[1]s, chains to it. It adds the proxy certificate to your trust rather than replacing it: it installs the certificate into the system trust store and, for tools that read a CA bundle instead, writes a bundle that is your existing trust plus the proxy certificate. Maven and Gradle get a truststore of their own instead, so the proxy's connections to registries trust the JVM truststore they would have loaded, as %[1]sglab dependency-firewall maven%[1]s and %[1]sglab dependency-firewall gradle%[1]s do. Like those commands, it warns when a truststore option that Maven or Gradle applies later, such as one in %[1]s_JAVA_OPTIONS%[1]s, would replace the truststore it gives them.
		`, "`") + text.ExperimentalString,
	}
	cmd.AddCommand(cmdStart.NewCmd(f))
	cmd.AddCommand(cmdStop.NewCmd(f))
	cmd.AddCommand(cmdServe.NewCmd(f))
	return cmd
}
