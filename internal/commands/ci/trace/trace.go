package trace

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdTrace(f cmdutils.Factory) *cobra.Command {
	pipelineCITraceCmd := &cobra.Command{
		Use:   "trace [<job-id>|<job-name>] [flags]",
		Short: `Trace a CI/CD job log in real time.`,
		Long: heredoc.Doc(`
			Streams the job log to the terminal. The output updates in real time
			while the job runs. Without a job argument, you can select one
			interactively.
		`),
		Example: heredoc.Doc(`
			# Interactively select a job to trace
			glab ci trace

			# Trace job with ID 224356863
			glab ci trace 224356863

			# Trace job with the name 'lint'
			glab ci trace lint`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}
			jobName := ""
			if len(args) != 0 {
				jobName = args[0]
			}
			branch, _ := cmd.Flags().GetString("branch")
			pipelineId, _ := cmd.Flags().GetInt("pipeline-id")

			return ciutils.TraceJob(cmd.Context(), &ciutils.JobInputs{
				JobName:    jobName,
				Branch:     branch,
				PipelineId: pipelineId,
			}, &ciutils.JobOptions{
				Client:     client,
				IO:         f.IO(),
				Repo:       repo,
				BranchFunc: f.Branch,
			})
		},
	}

	pipelineCITraceCmd.Flags().StringP("branch", "b", "", "The branch to search for the job. Defaults to the current branch.")
	pipelineCITraceCmd.Flags().IntP("pipeline-id", "p", 0, "The pipeline ID to search for the job.")
	return pipelineCITraceCmd
}
