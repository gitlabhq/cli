package df

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cmdBundle "gitlab.com/gitlab-org/cli/internal/commands/df/bundle"
	cmdCargo "gitlab.com/gitlab-org/cli/internal/commands/df/cargo"
	cmdCISummary "gitlab.com/gitlab-org/cli/internal/commands/df/cisummary"
	cmdGem "gitlab.com/gitlab-org/cli/internal/commands/df/gem"
	cmdGolang "gitlab.com/gitlab-org/cli/internal/commands/df/golang"
	cmdGradle "gitlab.com/gitlab-org/cli/internal/commands/df/gradle"
	cmdMaven "gitlab.com/gitlab-org/cli/internal/commands/df/maven"
	cmdNpm "gitlab.com/gitlab-org/cli/internal/commands/df/npm"
	cmdPackage "gitlab.com/gitlab-org/cli/internal/commands/df/package"
	cmdPip "gitlab.com/gitlab-org/cli/internal/commands/df/pip"
	cmdPipenv "gitlab.com/gitlab-org/cli/internal/commands/df/pipenv"
	cmdPnpm "gitlab.com/gitlab-org/cli/internal/commands/df/pnpm"
	cmdPoetry "gitlab.com/gitlab-org/cli/internal/commands/df/poetry"
	cmdTwine "gitlab.com/gitlab-org/cli/internal/commands/df/twine"
	cmdUv "gitlab.com/gitlab-org/cli/internal/commands/df/uv"
	cmdYarn "gitlab.com/gitlab-org/cli/internal/commands/df/yarn"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dependency-firewall <command>",
		Aliases: []string{"df"},
		Short:   "Configure and monitor GitLab Dependency Firewall for local package managers. (BETA)",
		Long: heredoc.Docf(`
			Commands to configure GitLab Dependency Firewall for local package
			managers, run local package managers with a summary of blocked or
			flagged packages, and view activity during the current session.

			Each package manager command, like %[1]sglab dependency-firewall npm%[1]s, runs the package manager binary and forwards every argument to it verbatim. While the package manager runs, the command:

			1. Starts a local proxy and routes the package manager's HTTPS traffic through it.
			1. Reads the name and version of each package the package manager downloads or publishes.
			1. Asks GitLab to evaluate the package against the policies for the project in your Git remote.
			1. Refuses blocked packages with a %[1]s403 Forbidden%[1]s response, and lets allowed and warned packages through.
			1. Records blocked and warned packages in %[1]s.gitlab/df/ci-log.json%[1]s and prints a summary.

			When the package manager finishes, the command prints a summary of the packages that matched your policies. A %[1]s✖%[1]s marks a blocked package, and a %[1]s▲%[1]s marks a package with a warning. If no package matched, the command prints no summary. A run with at least one blocked package prints the summary to standard error. A run with only warnings prints it to standard output.

			Evaluation results are cached for the length of a run, so a package that appears many times in a dependency tree is evaluated once.

			If GitLab can't evaluate a package, the command blocks it. If the Dependency Firewall isn't evaluating the project, the command allows every package.

			The command does not change your registry configuration. For the length of the run, it sets environment variables that route the package manager's traffic through the proxy and make the package manager trust the proxy certificate. Your existing CA bundles stay trusted.

			To reach package registries, the proxy uses the outbound proxy in %[1]sHTTPS_PROXY%[1]s, including any credentials in its URL. For plain-HTTP registries it uses %[1]sHTTP_PROXY%[1]s instead, when that's set. For npm and pnpm, %[1]sHTTPS_PROXY%[1]s takes precedence over %[1]sHTTP_PROXY%[1]s for plain-HTTP registries too, matching those tools. It connects directly to the hosts listed in %[1]sNO_PROXY%[1]s. The %[1]sproxy%[1]s setting in your glab configuration applies only to GitLab API calls.

			Some package managers have proxy environment variables of their own, such as %[1]snpm_config_https_proxy%[1]s, %[1]sYARN_HTTPS_PROXY%[1]s, %[1]sPIP_PROXY%[1]s, and %[1]sCARGO_HTTP_PROXY%[1]s. The proxy reads those variables first, so for those package managers the outbound proxy can differ from %[1]sHTTPS_PROXY%[1]s and %[1]sHTTP_PROXY%[1]s. A proxy from Yarn's or pip's own settings also applies to hosts in %[1]sNO_PROXY%[1]s, as it does in those tools.

			For npm, Yarn, pip, pipenv, and cargo, the proxy also reads the same settings from your user and global config files, such as %[1]s~/.npmrc%[1]s, %[1]s~/.yarnrc%[1]s, %[1]s~/.yarnrc.yml%[1]s, %[1]spip.conf%[1]s, and %[1]s$CARGO_HOME/config.toml%[1]s. It doesn't read proxy settings from files inside the project, such as a %[1]s.npmrc%[1]s or %[1]s.cargo/config.toml%[1]s in the repository, or a user or global config file stored there. Outside a Git repository, the working directory and everything under it count as the project.

			Connections to package registries trust the system certificates, the certificate in the %[1]sca_cert%[1]s setting for your GitLab host, and the CA bundles in %[1]sSSL_CERT_FILE%[1]s, %[1]sREQUESTS_CA_BUNDLE%[1]s, %[1]sPIP_CERT%[1]s, %[1]sTWINE_CERT%[1]s, %[1]sNODE_EXTRA_CA_CERTS%[1]s, %[1]sBUNDLE_SSL_CA_CERT%[1]s, and %[1]sCARGO_HTTP_CAINFO%[1]s. GitLab API calls trust only the system certificates and %[1]sca_cert%[1]s, so if your outbound proxy uses a private certificate authority, set %[1]sca_cert%[1]s to that CA to cover both. %[1]sglab dependency-firewall go%[1]s uses the same outbound proxy and certificates for module downloads from a module proxy. On macOS, on Windows, and when only %[1]sSSL_CERT_DIR%[1]s is set, %[1]sgo%[1]s keeps its own trust for them. Modules that %[1]sgo%[1]s fetches directly from version control use the certificate and proxy settings of %[1]sgo%[1]s and %[1]sgit%[1]s.

			The command writes %[1]s.gitlab/df/ci-log.json%[1]s to the working directory on every run, including local runs. Add %[1]s.gitlab/df/%[1]s to your %[1]s.gitignore%[1]s file.

			To run a package manager command in a GitLab CI/CD job, set %[1]sGLAB_ENABLE_CI_AUTOLOGIN%[1]s to %[1]strue%[1]s so the command signs in with the CI/CD job token. When the firewall blocks a package, the install fails, and so does the job. For more information, see [CI/CD auto-login](https://docs.gitlab.com/cli/authentication/#auto-login).

			%[1]s%[1]s%[1]syaml
			install:
			  variables:
			    GLAB_ENABLE_CI_AUTOLOGIN: "true"
			  script:
			    - glab dependency-firewall npm ci
			%[1]s%[1]s%[1]s

			Package manager commands exit with the following codes:

			| Exit code | Meaning |
			|-----------|---------|
			| The package manager exit code | The package manager ran and exited. |
			| %[1]s1%[1]s | The package manager was not found on your %[1]sPATH%[1]s or could not start. |
			| %[1]s130%[1]s | The run was interrupted or stopped, for example, with Control+C or when a CI/CD job is canceled. |
		`, "`") + text.BetaString,
	}

	cmd.AddCommand(cmdNpm.NewCmd(f))
	cmd.AddCommand(cmdPnpm.NewCmd(f))
	cmd.AddCommand(cmdYarn.NewCmd(f))
	cmd.AddCommand(cmdPip.NewCmd(f))
	cmd.AddCommand(cmdUv.NewCmd(f))
	cmd.AddCommand(cmdTwine.NewCmd(f))
	cmd.AddCommand(cmdPipenv.NewCmd(f))
	cmd.AddCommand(cmdPoetry.NewCmd(f))
	cmd.AddCommand(cmdMaven.NewCmd(f))
	cmd.AddCommand(cmdGradle.NewCmd(f))
	cmd.AddCommand(cmdGem.NewCmd(f))
	cmd.AddCommand(cmdBundle.NewCmd(f))
	cmd.AddCommand(cmdCargo.NewCmd(f))
	cmd.AddCommand(cmdGolang.NewCmd(f))
	cmd.AddCommand(cmdPackage.NewCmd(f))
	cmd.AddCommand(cmdCISummary.NewCmd(f))

	return cmd
}
