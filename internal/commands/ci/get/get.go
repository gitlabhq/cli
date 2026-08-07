package get

import (
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

const NoVariablesInPipelineMessage = "No variables found in pipeline."

type PipelineMergedResponse struct {
	*gitlab.Pipeline
	Jobs      []*gitlab.Job              `json:"jobs"`
	Variables []*gitlab.PipelineVariable `json:"variables"`
}

func NewCmdGet(f cmdutils.Factory) *cobra.Command {
	pipelineGetCmd := &cobra.Command{
		Use:     "get [flags]",
		Short:   `Get the details of a CI/CD pipeline.`,
		Aliases: []string{"stats"},
		Long: heredoc.Docf(`
			Defaults to the current branch. Use %[1]s--pipeline-id%[1]s to specify a pipeline
			instead of fetching the latest for a branch.

			Use %[1]s--merge-request%[1]s to target the head pipeline of a specific merge
			request by IID. This differs from %[1]s--branch%[1]s when the MR's head pipeline
			diverges from the latest pipeline on its source branch — for example, forks or
			detached pipelines.

			Use %[1]s--status%[1]s to filter jobs by state (passed through to the API's
			%[1]sscope%[1]s parameter).

			Use %[1]s--output json%[1]s to get the pipeline details as JSON.
		`, "`"),
		Example: heredoc.Doc(`
			# Get the pipeline for the current branch
			glab ci get

			# Get a specific pipeline by ID in another project
			glab ci get -R some/project -p 12345

			# Show only failed jobs for the head pipeline of MR !42
			glab ci get --merge-request=42 --status=failed --with-job-details`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			// Parse arguments into local vars
			branch, _ := cmd.Flags().GetString("branch")
			pipelineId, err := cmd.Flags().GetInt("pipeline-id")
			if err != nil {
				return err
			}
			mrIID, err := cmd.Flags().GetInt("merge-request")
			if err != nil {
				return err
			}

			var msgNotFound string
			if pipelineId != 0 {
				msgNotFound = fmt.Sprintf("No pipeline with the given ID: %d", pipelineId)
			} else if mrIID != 0 {
				mr, _, err := client.MergeRequests.GetMergeRequest(repo.FullName(), int64(mrIID), nil)
				if err != nil {
					return fmt.Errorf("failed to get merge request !%d: %w", mrIID, err)
				}
				if mr.HeadPipeline == nil || mr.HeadPipeline.ID == 0 {
					return fmt.Errorf("no pipeline found for merge request !%d", mrIID)
				}
				pipelineId = int(mr.HeadPipeline.ID)
				msgNotFound = fmt.Sprintf("No pipeline found for merge request !%d", mrIID)
			} else {
				// Use enhanced branch resolution that supports API fallback
				branch = ciutils.GetBranch(branch, func() (string, error) {
					return f.Branch()
				}, repo, client)

				// Use GetPipelineWithFallback for robust pipeline lookup with MR fallback
				pipeline, err := ciutils.GetPipelineWithFallback(cmd.Context(), client, repo.FullName(), branch, f.IO())
				if err != nil {
					return err
				}
				pipelineId = int(pipeline.ID)
				msgNotFound = fmt.Sprintf("No pipelines running or available on branch: %s", branch)
			}

			pipeline, _, err := client.Pipelines.GetPipeline(repo.FullName(), int64(pipelineId))
			if err != nil {
				return fmt.Errorf("%s: %w", msgNotFound, err)
			}

			statusFilter, _ := cmd.Flags().GetString("status")
			listOpts := &gitlab.ListJobsOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}
			if statusFilter != "" {
				listOpts.Scope = &[]gitlab.BuildStateValue{gitlab.BuildStateValue(statusFilter)}
			}

			jobs, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Job, *gitlab.Response, error) {
				return client.Jobs.ListPipelineJobs(repo.FullName(), int64(pipelineId), listOpts, p)
			})
			if err != nil {
				return err
			}

			showVariables, _ := cmd.Flags().GetBool("with-variables")

			var variables []*gitlab.PipelineVariable
			if showVariables {
				variables, _, err = client.Pipelines.GetPipelineVariables(pipeline.ProjectID, int64(pipelineId))
				if err != nil {
					return err
				}
			}

			mergedPipelineObject := &PipelineMergedResponse{
				Pipeline:  pipeline,
				Jobs:      jobs,
				Variables: variables,
			}

			outputFormat, _ := cmd.Flags().GetString("output-format")
			output, _ := cmd.Flags().GetString("output")
			if output == "json" || outputFormat == "json" {
				return f.IO().PrintJSON(*mergedPipelineObject)
			}

			showJobDetails, _ := cmd.Flags().GetBool("with-job-details")
			printTable(f.IO(), *mergedPipelineObject, showJobDetails)
			return nil
		},
	}

	fl := pipelineGetCmd.Flags()
	fl.StringP("branch", "b", "", "Get the pipeline for a branch. Defaults to the current branch.")
	fl.IntP("pipeline-id", "p", 0, "Get the pipeline with the given <id>.")
	fl.Int("merge-request", 0, "Show the pipeline for the given merge request <iid>.")
	pipelineGetCmd.MarkFlagsMutuallyExclusive("merge-request", "pipeline-id")
	pipelineGetCmd.MarkFlagsMutuallyExclusive("merge-request", "branch")
	fl.StringP("output", "F", "text", "Format output. Options: text, json.")
	fl.StringP("output-format", "o", "text", "Use output.")
	_ = fl.MarkHidden("output-format")
	_ = fl.MarkDeprecated("output-format", "Deprecated. Use 'output' instead.")
	fl.BoolP("with-job-details", "d", false, "Show extended job information.")
	fl.Bool("with-variables", false, "Show variables in pipeline. Requires the Maintainer role.")
	fl.StringP("status", "s", "", "Show only jobs in the given state. Passed through to the API's scope parameter.")

	cmdutils.AddJQFlag(pipelineGetCmd, f.IO())
	return pipelineGetCmd
}

func printTable(io *iostreams.IOStreams, p PipelineMergedResponse, showJobDetails bool) {
	printPipelineTable(io, p)

	if showJobDetails {
		printJobTable(io, p)
	} else {
		printJobText(io, p)
	}

	printVariables(io, p)
}

func printPipelineTable(io *iostreams.IOStreams, p PipelineMergedResponse) {
	io.LogInfof("%s", "# Pipeline:\n")
	pipelineTable := tableprinter.NewTablePrinter()
	pipelineTable.AddRow("id:", strconv.FormatInt(p.ID, 10))
	pipelineTable.AddRow("status:", p.Status)
	pipelineTable.AddRow("source:", p.Source)
	pipelineTable.AddRow("ref:", p.Ref)
	pipelineTable.AddRow("sha:", p.SHA)
	pipelineTable.AddRow("tag:", p.Tag)
	pipelineTable.AddRow("yaml Errors:", p.YamlErrors)
	pipelineTable.AddRow("user:", p.User.Username)
	pipelineTable.AddRow("created:", p.CreatedAt)
	pipelineTable.AddRow("started:", p.StartedAt)
	pipelineTable.AddRow("updated:", p.UpdatedAt)
	io.LogInfo(pipelineTable.String())
}

func printJobTable(io *iostreams.IOStreams, p PipelineMergedResponse) {
	io.LogInfof("%s", "# Jobs:\n")
	jobTable := tableprinter.NewTablePrinter()
	jobTable.AddRow("ID", "Name", "Stage", "Status", "Duration", "Failure reason", "URL")
	for _, j := range p.Jobs {
		jobTable.AddRow(j.ID, j.Name, j.Stage, j.Status, j.Duration, j.FailureReason, j.WebURL)
	}
	io.LogInfo(jobTable.String())
}

func printJobText(io *iostreams.IOStreams, p PipelineMergedResponse) {
	io.LogInfof("%s", "# Jobs:\n")
	jobTable := tableprinter.NewTablePrinter()
	for _, j := range p.Jobs {
		jobTable.AddRow(j.Name+":", j.Status)
	}
	io.LogInfo(jobTable.String())
}

func printVariables(io *iostreams.IOStreams, p PipelineMergedResponse) {
	if p.Variables != nil {
		io.LogInfof("%s", "# Variables:\n")
		if len(p.Variables) == 0 {
			io.LogInfof("%s", NoVariablesInPipelineMessage)
		}

		varTable := tableprinter.NewTablePrinter()
		for _, v := range p.Variables {
			varTable.AddRow(v.Key+":", v.Value)
		}
		io.LogInfo(varTable.String())
	}
}
