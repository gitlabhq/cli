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
