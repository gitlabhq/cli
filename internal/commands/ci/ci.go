package ci

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	jobArtifactCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/artifact"
	ciCancelCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/cancel"
	ciConfigCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/config"
	pipeDeleteCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/delete"
	pipeGetCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/get"
	legacyCICmd "gitlab.com/gitlab-org/cli/internal/commands/ci/legacyci"
	ciLintCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/lint"
	pipeListCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/list"
	pipeRetryCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/retry"
	pipeRunCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/run"
	pipeRunTrigCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/run_trig"
	pipeStatusCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/status"
	ciTraceCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/trace"
	jobPlayCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/trigger"
	ciViewCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/view"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdCI(f cmdutils.Factory) *cobra.Command {
	ciCmd := &cobra.Command{
		Use:   "ci <command> [flags]",
		Short: `Work with GitLab CI/CD pipelines and jobs.`,
		Long: heredoc.Docf(`
		TEST DO NOT MERGE
		Manages CI/CD pipelines and jobs in your GitLab project.

		Use these commands to manage CI/CD pipelines and jobs. You can also
		lint and compile CI/CD configuration files.

		The %[1]spipe%[1]s and %[1]spipeline%[1]s aliases are deprecated. Use %[1]sci%[1]s instead.
		`, "`"),
		Aliases: []string{"pipe", "pipeline"},
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		Run: func(cmd *cobra.Command, args []string) {
			f.IO().LogErrorf("Aliases 'pipe' and 'pipeline' are deprecated. Use 'ci' instead.\n\n")
			_ = cmd.Help()
		},
	}

	cmdutils.EnableRepoOverride(ciCmd, f)
	ciCmd.AddCommand(legacyCICmd.NewCmdCI(f))
	ciCmd.AddCommand(ciTraceCmd.NewCmdTrace(f))
	ciCmd.AddCommand(ciViewCmd.NewCmdView(f))
	ciCmd.AddCommand(ciLintCmd.NewCmdLint(f))
	ciCmd.AddCommand(ciCancelCmd.NewCmdCancel(f))
	ciCmd.AddCommand(pipeDeleteCmd.NewCmdDelete(f))
	ciCmd.AddCommand(pipeListCmd.NewCmdList(f))
	ciCmd.AddCommand(pipeStatusCmd.NewCmdStatus(f))
	ciCmd.AddCommand(pipeRetryCmd.NewCmdRetry(f))
	ciCmd.AddCommand(pipeRunCmd.NewCmdRun(f))
	ciCmd.AddCommand(jobPlayCmd.NewCmdTrigger(f))
	ciCmd.AddCommand(pipeRunTrigCmd.NewCmdRunTrig(f))
	ciCmd.AddCommand(jobArtifactCmd.NewCmdRun(f))
	ciCmd.AddCommand(pipeGetCmd.NewCmdGet(f))
	ciCmd.AddCommand(ciConfigCmd.NewCmdConfig(f))

	return ciCmd
}
