package retry

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdRetry(f cmdutils.Factory) *cobra.Command {
	pipelineRetryCmd := &cobra.Command{
		Use:     "retry <job-id>",
		Short:   `Retry a CI/CD job.`,
		Aliases: []string{},
		Long: heredoc.Doc(`
			Without a job argument, you can select one interactively.
		`),
		Example: heredoc.Doc(`
			# Interactively select a job to retry
			glab ci retry

			# Retry job with ID 224356863
			glab ci retry 224356863

			# Retry job with the name 'lint'
			glab ci retry lint`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
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

			jobID, err := ciutils.GetJobId(cmd.Context(), &ciutils.JobInputs{
				JobName:         jobName,
				Branch:          branch,
				PipelineId:      pipelineId,
				SelectionPrompt: "Select pipeline job to retry:",
			}, &ciutils.JobOptions{
				Client:     client,
				IO:         f.IO(),
				Repo:       repo,
				BranchFunc: f.Branch,
			})
			if err != nil {
				if jobName != "" {
					fmt.Fprintln(f.IO().StdErr, "invalid job ID:", jobName)
				}
				return err
			}

			if jobID == 0 {
				return nil
			}

			job, _, err := client.Jobs.RetryJob(repo.FullName(), jobID)
			if err != nil {
				return cmdutils.WrapError(err, fmt.Sprintf("Could not retry job with ID: %d", jobID))
			}
			output := fmt.Sprintf("Retried job (ID: %d), status: %s, ref: %s, weburl: %s", job.ID, job.Status, job.Ref, job.WebURL)
			fmt.Fprintln(f.IO().StdOut, output)

			return nil
		},
	}

	pipelineRetryCmd.Flags().StringP("branch", "b", "", "The branch to search for the job. (default current branch)")
	pipelineRetryCmd.Flags().IntP("pipeline-id", "p", 0, "The pipeline ID to search for the job.")
	return pipelineRetryCmd
}
