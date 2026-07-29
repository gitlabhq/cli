package ciutils

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/huh/v2"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func makeHyperlink(s *iostreams.IOStreams, pipeline *gitlab.PipelineInfo) string {
	return s.Hyperlink(fmt.Sprintf("%d", pipeline.ID), pipeline.WebURL)
}

// GetPipelineWithFallback gets the latest pipeline for a branch, falling back to MR head pipeline
// for merged results pipelines where the direct branch lookup may fail or returns a pipeline with no jobs.
func GetPipelineWithFallback(ctx context.Context, client *gitlab.Client, repoName, branch string, ios *iostreams.IOStreams) (*gitlab.Pipeline, error) {
	// First try: Get pipeline by branch name
	pipeline, _, err := client.Pipelines.GetLatestPipeline(repoName, &gitlab.GetLatestPipelineOptions{Ref: new(branch)}, gitlab.WithContext(ctx))
	if err == nil {
		// Check if the pipeline has jobs - some pipelines (e.g., external pipelines) may have no jobs
		jobs, _, jobsErr := client.Jobs.ListPipelineJobs(repoName, pipeline.ID, &gitlab.ListJobsOptions{
			ListOptions: gitlab.ListOptions{PerPage: 1},
		})
		if jobsErr == nil && len(jobs) > 0 {
			// Pipeline has jobs, return it
			return pipeline, nil
		}
		// Pipeline has no jobs, try MR fallback below
	}

	// Fallback: Look for MR pipeline (for merged results pipelines or when branch pipeline has no jobs)
	mr, mrErr := getMRForBranch(ctx, client, repoName, branch, ios)
	if mrErr != nil {
		// If we had a pipeline from the branch lookup (even with no jobs), return it
		if pipeline != nil {
			return pipeline, nil
		}
		return nil, fmt.Errorf("no pipeline found for branch %s and failed to find associated merge request: %w", branch, mrErr)
	}

	if mr.HeadPipeline == nil {
		// If we had a pipeline from the branch lookup (even with no jobs), return it
		if pipeline != nil {
			return pipeline, nil
		}
		return nil, fmt.Errorf("no pipeline found. It might not exist yet. Check your pipeline configuration")
	}

	// Get the full pipeline details using the MR's head pipeline ID
	mrPipeline, _, pipelineErr := client.Pipelines.GetPipeline(repoName, mr.HeadPipeline.ID, gitlab.WithContext(ctx))
	if pipelineErr != nil {
		// If we had a pipeline from the branch lookup, return it as fallback
		if pipeline != nil {
			return pipeline, nil
		}
		return nil, pipelineErr
	}

	return mrPipeline, nil
}

// getMRForBranch finds a merge request for the given branch
func getMRForBranch(ctx context.Context, client *gitlab.Client, repoName, branch string, ios *iostreams.IOStreams) (*gitlab.MergeRequest, error) {
	opts := &gitlab.ListProjectMergeRequestsOptions{
		SourceBranch: new(branch),
	}

	mrs, err := api.ListMRs(client, repoName, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get merge requests for %q: %w", branch, err)
	}

	if len(mrs) == 0 {
		return nil, fmt.Errorf("no merge request available for %q", branch)
	}

	var selectedMR *gitlab.BasicMergeRequest

	// If exactly one MR, use it
	if len(mrs) == 1 {
		selectedMR = mrs[0]
	} else {
		// Multiple MRs exist - need to handle selection
		if ios == nil || !ios.PromptEnabled() {
			// Build error message with list of possible MRs
			var mrNames []string
			for _, mr := range mrs {
				mrNames = append(mrNames, fmt.Sprintf("!%d (%s) by @%s", mr.IID, branch, mr.Author.Username))
			}
			return nil, fmt.Errorf("merge request ID number required. Possible matches:\n\n%s", strings.Join(mrNames, "\n"))
		}

		// Prompt user to select
		mrMap := map[string]*gitlab.BasicMergeRequest{}
		var mrNames []string
		for i := range mrs {
			t := fmt.Sprintf("!%d (%s) by @%s", mrs[i].IID, branch, mrs[i].Author.Username)
			mrMap[t] = mrs[i]
			mrNames = append(mrNames, t)
		}

		pickedMR := mrNames[0]
		err = ios.Select(ctx, &pickedMR, "Multiple merge requests exist for this branch. Select one:", mrNames)
		if err != nil {
			return nil, fmt.Errorf("you must select a merge request: %w", err)
		}

		selectedMR = mrMap[pickedMR]
	}

	// Fetch the full MR to get HeadPipeline
	fullMR, _, err := client.MergeRequests.GetMergeRequest(repoName, selectedMR.IID, &gitlab.GetMergeRequestsOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get merge request details: %w", err)
	}

	return fullMR, nil
}

func DisplaySchedules(i *iostreams.IOStreams, s []*gitlab.PipelineSchedule, projectID string) string {
	if len(s) > 0 {
		table := tableprinter.NewTablePrinter()
		table.AddRow("ID", "Description", "Cron", "Owner", "Active")
		for _, schedule := range s {
			table.AddRow(schedule.ID, schedule.Description, schedule.Cron, schedule.Owner.Username, schedule.Active)
		}

		return table.Render()
	}

	// return empty string, since when there is no schedule, the title will already display it accordingly
	return ""
}

func DisplayMultiplePipelines(s *iostreams.IOStreams, p []*gitlab.PipelineInfo, projectID string) string {
	if len(p) == 0 {
		return "No Pipelines available on " + projectID
	}

	c := s.Color()

	table := tableprinter.NewTablePrinter()
	table.AddRow("State", "IID", "Ref", "Created")

	for _, pipeline := range p {
		duration := ""
		if pipeline.CreatedAt != nil {
			duration = c.Magenta("(" + utils.TimeToPrettyTimeAgo(*pipeline.CreatedAt) + ")")
		}

		pipeState := fmt.Sprintf("(%s) • #%s", pipeline.Status, makeHyperlink(s, pipeline))
		switch pipeline.Status {
		case string(gitlab.Running):
			pipeState = c.Blue(pipeState)
		case string(gitlab.Success):
			pipeState = c.Green(pipeState)
		case string(gitlab.Failed):
			pipeState = c.Red(pipeState)
		default:
			pipeState = c.Gray(pipeState)
		}

		table.AddRow(pipeState, fmt.Sprintf("(#%d)", pipeline.IID), pipeline.Ref, duration)
	}

	return table.Render()
}

func RunTraceSha(ctx context.Context, apiClient *gitlab.Client, w io.Writer, pid any, sha, name string) error {
	job, err := api.PipelineJobWithSha(apiClient, pid, sha, name)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}
	if job == nil {
		return fmt.Errorf("failed to find job: no matching job named %q for this pipeline", name)
	}
	return runTrace(ctx, apiClient, w, pid, job.ID, 3*time.Second)
}

func runTrace(ctx context.Context, apiClient *gitlab.Client, w io.Writer, pid any, jobId int64, pollInterval time.Duration) error {
	var once sync.Once
	var offset int64

	fmt.Fprintln(w, "Getting job trace...") //nolint:forbidigo // w is a generic io.Writer, not always StdOut/StdErr
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		job, _, err := apiClient.Jobs.GetJob(pid, jobId)
		if err != nil {
			return fmt.Errorf("failed to find job: %w", err)
		}
		switch job.Status {
		case string(gitlab.Pending):
			fmt.Fprintf(w, "%s is pending... waiting for job to start.\n", job.Name) //nolint:forbidigo // w is a generic io.Writer, not always StdOut/StdErr
			continue
		case string(gitlab.Manual):
			fmt.Fprintf(w, "Manual job %s not started, waiting for job to start.\n", job.Name) //nolint:forbidigo // w is a generic io.Writer, not always StdOut/StdErr
			continue
		case string(gitlab.Skipped):
			fmt.Fprintf(w, "%s has been skipped.\n", job.Name) //nolint:forbidigo // w is a generic io.Writer, not always StdOut/StdErr
		}
		once.Do(func() {
			fmt.Fprintf(w, "Showing logs for %s job #%d.\n", job.Name, job.ID) //nolint:forbidigo // w is a generic io.Writer, not always StdOut/StdErr
		})
		trace, _, err := apiClient.Jobs.GetTraceFile(pid, jobId)
		if err != nil {
			return fmt.Errorf("failed to find job: %w", err)
		}
		if trace == nil {
			return fmt.Errorf("failed to find job: trace response was empty for job %d", jobId)
		}
		_, _ = io.CopyN(io.Discard, trace, offset)
		lenT, err := io.Copy(w, trace)
		if err != nil {
			return err
		}
		offset += lenT

		if job.Status == string(gitlab.Success) ||
			job.Status == string(gitlab.Failed) ||
			job.Status == string(gitlab.Canceled) {
			return nil
		}
	}
}

func GetJobId(ctx context.Context, inputs *JobInputs, opts *JobOptions) (int64, error) {
	// If the user hasn't supplied an argument, we display the jobs list interactively.
	if inputs.JobName == "" {
		return getJobIdInteractive(ctx, inputs, opts)
	}

	// If the user supplied a job ID, we can use it directly.
	if jobID, err := strconv.Atoi(inputs.JobName); err == nil {
		return int64(jobID), nil
	}

	// Otherwise, we try to find the latest job ID based on the job name.
	pipelineId, err := getPipelineId(ctx, inputs, opts)
	if err != nil {
		return 0, fmt.Errorf("get pipeline: %w", err)
	}

	// This is also the default
	jobs := make([]*gitlab.Job, 0)
	options := &gitlab.ListJobsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 20,
			Page:    1,
		},
	}

	for {
		jobsPerPage, response, err := opts.Client.Jobs.ListPipelineJobs(opts.Repo.FullName(), pipelineId, options)
		if err != nil {
			return 0, fmt.Errorf("list pipeline jobs: %w", err)
		}
		jobs = append(jobs, jobsPerPage...)

		// indicate that we have reached the last page
		if response.NextPage == 0 {
			break
		}

		options.Page = response.NextPage
	}

	if len(jobs) == 0 {
		return 0, fmt.Errorf("pipeline %d contains no jobs at all", pipelineId)
	}

	for _, job := range jobs {
		if job.Name == inputs.JobName {
			return job.ID, nil
		}
	}

	return 0, fmt.Errorf("pipeline %d contains no jobs with the name %s", pipelineId, inputs.JobName)
}

func getPipelineId(ctx context.Context, inputs *JobInputs, opts *JobOptions) (int64, error) {
	if inputs.PipelineId != 0 {
		return int64(inputs.PipelineId), nil
	}

	branch := GetBranch(inputs.Branch, opts.BranchFunc, opts.Repo, opts.Client)
	if branch == "" {
		return 0, fmt.Errorf("unable to determine branch")
	}

	// Use fallback logic for robust pipeline lookup including MR pipelines
	pipeline, err := GetPipelineWithFallback(ctx, opts.Client, opts.Repo.FullName(), branch, opts.IO)
	if err != nil {
		return 0, fmt.Errorf("failed to get pipeline for branch %s: %w", branch, err)
	}
	return pipeline.ID, nil
}

// GetDefaultBranch fetches the repository's default branch from GitLab API.
// Falls back to git.DefaultBranchName if the API call fails or returns empty.
func GetDefaultBranch(repo glrepo.Interface, client *gitlab.Client) string {
	if repo == nil || client == nil {
		return git.DefaultBranchName
	}
	project, err := api.GetProject(client, repo.FullName())
	if err != nil || project.DefaultBranch == "" {
		return git.DefaultBranchName
	}
	return project.DefaultBranch
}

// GetBranch returns the specified branch, current git branch, or the default branch from API
func GetBranch(branch string, currentBranch func() (string, error), repo glrepo.Interface, client *gitlab.Client) string {
	if branch != "" {
		return branch
	}
	if currentBranch != nil {
		if gitBranch, _ := currentBranch(); gitBranch != "" {
			return gitBranch
		}
	}
	return GetDefaultBranch(repo, client)
}

func getJobIdInteractive(ctx context.Context, inputs *JobInputs, opts *JobOptions) (int64, error) {
	pipelineId, err := getPipelineId(ctx, inputs, opts)
	if err != nil {
		return 0, err
	}

	opts.IO.LogInfof("Getting jobs for pipeline %d...\n\n", pipelineId)

	listOptions := &gitlab.ListJobsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
		},
	}
	jobs, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Job, *gitlab.Response, error) {
		return opts.Client.Jobs.ListPipelineJobs(opts.Repo.FullName(), pipelineId, listOptions, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return 0, err
	}

	options := make([]huh.Option[int64], 0)
	for _, job := range jobs {
		if inputs.SelectionPredicate == nil || inputs.SelectionPredicate(job) {
			label := fmt.Sprintf("%s (%d) - %s", job.Name, job.ID, job.Status)
			options = append(options, huh.NewOption(label, job.ID))
		}
	}

	if len(options) == 0 {
		pipeline, _, err := opts.Client.Pipelines.GetPipeline(opts.Repo.FullName(), pipelineId)
		if err != nil {
			return 0, err
		}
		// use commit statuses to show external jobs
		cs, _, err := opts.Client.Commits.GetCommitStatuses(opts.Repo.FullName(), pipeline.SHA, &gitlab.GetCommitStatusesOptions{All: new(true)})
		if err != nil {
			return 0, err
		}

		c := opts.IO.Color()

		opts.IO.LogInfof("%s", "Getting external jobs...\n")
		for _, status := range cs {
			var s string

			switch status.Status {
			case string(gitlab.Success):
				s = c.Green(status.Status)
			case "error":
				s = c.Red(status.Status)
			default:
				s = c.Gray(status.Status)
			}
			opts.IO.LogInfof("(%s) %s\nURL: %s\n\n", s, c.Bold(status.Name), c.Gray(status.TargetURL))
		}

		opts.IO.LogError("Pipeline has no jobs or external statuses. " +
			"Check for errors in your '.gitlab-ci.yml' and your pipeline configuration.")
		return 0, nil
	}

	messagePrompt := inputs.SelectionPrompt
	if messagePrompt == "" {
		messagePrompt = "Select pipeline job to trace:"
	}

	var selectedJobID int64
	selector := huh.NewSelect[int64]().
		Title(messagePrompt).
		Options(options...).
		Value(&selectedJobID)

	err = opts.IO.Run(ctx, selector)
	if err != nil {
		return 0, err
	}

	return selectedJobID, nil
}

type JobInputs struct {
	JobName            string
	Branch             string
	PipelineId         int
	SelectionPrompt    string
	SelectionPredicate func(s *gitlab.Job) bool
}

type JobOptions struct {
	Client       *gitlab.Client
	Repo         glrepo.Interface
	IO           *iostreams.IOStreams
	BranchFunc   func() (string, error)
	PollInterval time.Duration // interval between trace polls; defaults to 3s if zero
}

func TraceJob(ctx context.Context, inputs *JobInputs, opts *JobOptions) error {
	jobID, err := GetJobId(ctx, inputs, opts)
	if err != nil {
		opts.IO.LogError("invalid job ID:", inputs.JobName)
		return err
	}
	if jobID == 0 {
		return nil
	}
	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = 3 * time.Second
	}
	opts.IO.LogInfo()
	return runTrace(ctx, opts.Client, opts.IO.StdOut, opts.Repo.FullName(), jobID, pollInterval)
}

// IDsFromArgs parses list of IDs from space or comma-separated values
func IDsFromArgs(args []string) ([]int, error) {
	var parsedValues []int

	f := func(r rune) bool {
		return r == ',' || r == ' '
	}

	processed := strings.FieldsFunc(strings.Join(args, " "), f)
	for _, v := range processed {
		id, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		parsedValues = append(parsedValues, id)
	}
	return parsedValues, nil
}
