package trigger

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdTrigger(f cmdutils.Factory) *cobra.Command {
	pipelineTriggerCmd := &cobra.Command{
		Use:     "trigger <job-id>",
		Short:   `Trigger a manual CI/CD job.`,
		Aliases: []string{},
		Long: heredoc.Doc(`
			Without a job argument, you can select one interactively.
			You can trigger only jobs with manual status.
		`),
		Example: heredoc.Doc(`
			# Interactively select a job to trigger
			glab ci trigger

			# Trigger manual job with id 224356863
			glab ci trigger 224356863

			# Trigger manual job with name lint
			glab ci trigger lint`),
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
				SelectionPrompt: "Select pipeline job to trigger:",
				SelectionPredicate: func(s *gitlab.Job) bool {
					return s.Status == "manual"
				},
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

			job, _, err := client.Jobs.PlayJob(repo.FullName(), jobID, &gitlab.PlayJobOptions{})
			if err != nil {
				return cmdutils.WrapError(err, fmt.Sprintf("Could not trigger job with ID: %d", jobID))
			}
			output := fmt.Sprintf("Triggered job (ID: %d), status: %s, ref: %s, weburl: %s", job.ID, job.Status, job.Ref, job.WebURL)
			fmt.Fprintln(f.IO().StdOut, output)

			return nil
		},
	}

	pipelineTriggerCmd.Flags().StringP("branch", "b", "", "The branch to search for the job. (default current branch)")
	pipelineTriggerCmd.Flags().IntP("pipeline-id", "p", 0, "The pipeline ID to search for the job.")
	return pipelineTriggerCmd
}
