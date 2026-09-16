package commands

import (
	"errors"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	aliasCmd "gitlab.com/gitlab-org/cli/internal/commands/alias"
	apiCmd "gitlab.com/gitlab-org/cli/internal/commands/api"
	artifactRegistryCmd "gitlab.com/gitlab-org/cli/internal/commands/artifactregistry"
	attestationCmd "gitlab.com/gitlab-org/cli/internal/commands/attestation"
	authCmd "gitlab.com/gitlab-org/cli/internal/commands/auth"
	changelogCmd "gitlab.com/gitlab-org/cli/internal/commands/changelog"
	pipelineCmd "gitlab.com/gitlab-org/cli/internal/commands/ci"
	clusterCmd "gitlab.com/gitlab-org/cli/internal/commands/cluster"
	completionCmd "gitlab.com/gitlab-org/cli/internal/commands/completion"
	configCmd "gitlab.com/gitlab-org/cli/internal/commands/config"
	containerRegistryCmd "gitlab.com/gitlab-org/cli/internal/commands/container_registry"
	deployKeyCmd "gitlab.com/gitlab-org/cli/internal/commands/deploy-key"
	dfCmd "gitlab.com/gitlab-org/cli/internal/commands/df"
	duoCmd "gitlab.com/gitlab-org/cli/internal/commands/duo"
	governCmd "gitlab.com/gitlab-org/cli/internal/commands/govern"
	gpgCmd "gitlab.com/gitlab-org/cli/internal/commands/gpg-key"
	"gitlab.com/gitlab-org/cli/internal/commands/help"
	incidentCmd "gitlab.com/gitlab-org/cli/internal/commands/incident"
	issueCmd "gitlab.com/gitlab-org/cli/internal/commands/issue"
	iterationCmd "gitlab.com/gitlab-org/cli/internal/commands/iteration"
	jobCmd "gitlab.com/gitlab-org/cli/internal/commands/job"
	labelCmd "gitlab.com/gitlab-org/cli/internal/commands/label"
	mcpCmd "gitlab.com/gitlab-org/cli/internal/commands/mcp"
	milestoneCmd "gitlab.com/gitlab-org/cli/internal/commands/milestone"
	mrCmd "gitlab.com/gitlab-org/cli/internal/commands/mr"
	opentofuCmd "gitlab.com/gitlab-org/cli/internal/commands/opentofu"
	orbitCmd "gitlab.com/gitlab-org/cli/internal/commands/orbit"
	packagesCmd "gitlab.com/gitlab-org/cli/internal/commands/packages"
	projectCmd "gitlab.com/gitlab-org/cli/internal/commands/project"
	releaseCmd "gitlab.com/gitlab-org/cli/internal/commands/release"
	runnerCmd "gitlab.com/gitlab-org/cli/internal/commands/runner"
	runnerControllerCmd "gitlab.com/gitlab-org/cli/internal/commands/runnercontroller"
	scheduleCmd "gitlab.com/gitlab-org/cli/internal/commands/schedule"
	searchCmd "gitlab.com/gitlab-org/cli/internal/commands/search"
	securefileCmd "gitlab.com/gitlab-org/cli/internal/commands/securefile"
	securityCmd "gitlab.com/gitlab-org/cli/internal/commands/security"
	skillsCmd "gitlab.com/gitlab-org/cli/internal/commands/skills"
	snippetCmd "gitlab.com/gitlab-org/cli/internal/commands/snippet"
	sshCmd "gitlab.com/gitlab-org/cli/internal/commands/ssh-key"
	stackCmd "gitlab.com/gitlab-org/cli/internal/commands/stack"
	todoCmd "gitlab.com/gitlab-org/cli/internal/commands/todo"
	tokenCmd "gitlab.com/gitlab-org/cli/internal/commands/token"
	updateCmd "gitlab.com/gitlab-org/cli/internal/commands/update"
	userCmd "gitlab.com/gitlab-org/cli/internal/commands/user"
	variableCmd "gitlab.com/gitlab-org/cli/internal/commands/variable"
	versionCmd "gitlab.com/gitlab-org/cli/internal/commands/version"
	whatsnewCmd "gitlab.com/gitlab-org/cli/internal/commands/whatsnew"
	workitemsCmd "gitlab.com/gitlab-org/cli/internal/commands/workitems"
	"gitlab.com/gitlab-org/cli/internal/config/confighelp"
)

// NewCmdRoot is the main root/parent command
func NewCmdRoot(f cmdutils.Factory) *cobra.Command {
	c := f.IO().Color()
	rootCmd := &cobra.Command{
		Use:           "glab <command> <subcommand> [flags]",
		Short:         "A GitLab CLI tool.",
		Long:          `GLab is an open source GitLab CLI tool that brings GitLab to your command line.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Annotations: map[string]string{
			"help:environment": confighelp.EnvironmentHelp(),
			"help:feedback": heredoc.Docf(`
			Encountered a bug or want to suggest a feature?
			Open an issue using '%s'
		`, c.Bold(c.Yellow("glab issue create -R gitlab-org/cli"))),
		},
	}

	// We deliberately do NOT call rootCmd.SetOut / rootCmd.SetErr here.
	// Cobra's c.Print() (used for deprecation warnings, "unknown help topic",
	// usage-on-error, etc.) routes through OutOrStderr, which returns Out when
	// it is set and falls back to os.Stderr otherwise. Wiring Out to StdOut
	// pollutes the stdout data channel with diagnostics — see
	// https://gitlab.com/gitlab-org/cli/-/issues/8371 and
	// https://github.com/spf13/cobra/issues/1708. Instead, the help and usage
	// funcs receive the IOStreams explicitly and route their own output.

	rootCmd.PersistentFlags().BoolP("help", "h", false, "Show help for this command.")
	rootCmd.SetHelpFunc(func(command *cobra.Command, args []string) {
		help.RootHelpFunc(f.IO(), command, args)
	})
	rootCmd.SetUsageFunc(func(command *cobra.Command) error {
		return help.RootUsageFunc(f.IO().StdErr, command)
	})
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if errors.Is(err, pflag.ErrHelp) {
			return err
		}
		return &cmdutils.FlagError{Err: err}
	})

	buildInfo := f.BuildInfo()
	formattedVersion := versionCmd.Scheme(buildInfo.Version, buildInfo.Commit)
	rootCmd.SetVersionTemplate(formattedVersion)
	rootCmd.Version = formattedVersion

	// Child commands
	rootCmd.AddCommand(aliasCmd.NewCmdAlias(f))
	rootCmd.AddCommand(configCmd.NewCmdConfig(f))
	rootCmd.AddCommand(completionCmd.NewCmdCompletion(f.IO()))
	rootCmd.AddCommand(versionCmd.NewCmdVersion(f))
	rootCmd.AddCommand(updateCmd.NewCheckUpdateCmd(f))
	rootCmd.AddCommand(whatsnewCmd.NewCmd(f))
	rootCmd.AddCommand(authCmd.NewCmdAuth(f))

	rootCmd.AddCommand(apiCmd.NewCmdApi(f, nil))
	rootCmd.AddCommand(artifactRegistryCmd.NewCmd(f))
	rootCmd.AddCommand(changelogCmd.NewCmdChangelog(f))
	rootCmd.AddCommand(clusterCmd.NewCmdCluster(f))
	rootCmd.AddCommand(containerRegistryCmd.NewCmd(f))
	rootCmd.AddCommand(deployKeyCmd.NewCmdDeployKey(f))
	rootCmd.AddCommand(dfCmd.NewCmd(f))
	rootCmd.AddCommand(duoCmd.NewCmd(f))
	rootCmd.AddCommand(governCmd.NewCmd(f))
	rootCmd.AddCommand(gpgCmd.NewCmdGPGKey(f))
	rootCmd.AddCommand(incidentCmd.NewCmdIncident(f))
	rootCmd.AddCommand(issueCmd.NewCmdIssue(f))
	rootCmd.AddCommand(iterationCmd.NewCmdIteration(f))
	rootCmd.AddCommand(jobCmd.NewCmdJob(f))
	rootCmd.AddCommand(labelCmd.NewCmdLabel(f))
	rootCmd.AddCommand(mcpCmd.NewCmdMCP(f))
	rootCmd.AddCommand(milestoneCmd.NewCmdMilestone(f))
	rootCmd.AddCommand(mrCmd.NewCmdMR(f))
	rootCmd.AddCommand(opentofuCmd.NewCmd(f))
	rootCmd.AddCommand(orbitCmd.NewCmd(f))
	rootCmd.AddCommand(packagesCmd.NewCmd(f))
	rootCmd.AddCommand(attestationCmd.NewCmdAttestation(f))
	rootCmd.AddCommand(pipelineCmd.NewCmdCI(f))
	rootCmd.AddCommand(projectCmd.NewCmdRepo(f))
	rootCmd.AddCommand(releaseCmd.NewCmdRelease(f))
	rootCmd.AddCommand(runnerCmd.NewCmdRunner(f))
	rootCmd.AddCommand(scheduleCmd.NewCmdSchedule(f))
	rootCmd.AddCommand(searchCmd.NewCmd(f))
	rootCmd.AddCommand(securefileCmd.NewCmdSecurefile(f))
	rootCmd.AddCommand(securityCmd.NewCmd(f))
	rootCmd.AddCommand(snippetCmd.NewCmdSnippet(f))
	rootCmd.AddCommand(sshCmd.NewCmdSSHKey(f))
	rootCmd.AddCommand(stackCmd.NewCmdStack(f))
	rootCmd.AddCommand(todoCmd.NewCmd(f))
	rootCmd.AddCommand(tokenCmd.NewTokenCmd(f))
	rootCmd.AddCommand(userCmd.NewCmdUser(f))
	rootCmd.AddCommand(variableCmd.NewVariableCmd(f))
	rootCmd.AddCommand(runnerControllerCmd.NewCmd(f))
	rootCmd.AddCommand(skillsCmd.NewCmdSkills(f))
	rootCmd.AddCommand(workitemsCmd.NewCmdWorkItems(f))
	// TODO: This can probably be removed by GitLab 18.3
	// See: https://gitlab.com/gitlab-org/cli/-/issues/7885
	// Add global repo override flag but keep it hidden
	cmdutils.AddGlobalRepoOverride(rootCmd, f)

	rootCmd.Flags().BoolP("version", "v", false, "Show glab version information.")
	return rootCmd
}
