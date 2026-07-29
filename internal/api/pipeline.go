package api

import (
	"sort"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

func PlayOrRetryJobs(client *gitlab.Client, repo string, jobID int64, status string) (*gitlab.Job, error) {
	switch status {
	case "pending", "running":
		return nil, nil
	case "manual":
		j, _, err := client.Jobs.PlayJob(repo, jobID, &gitlab.PlayJobOptions{})
		if err != nil {
			return nil, err
		}
		return j, nil
	default:

		j, _, err := client.Jobs.RetryJob(repo, jobID)
		if err != nil {
			return nil, err
		}

		return j, nil
	}
}

func PipelineJobWithSha(client *gitlab.Client, pid any, sha, name string) (*gitlab.Job, error) {
	jobs, _, err := pipelineJobsWithSha(client, pid, sha)
	if len(jobs) == 0 || err != nil {
		return nil, err
	}
	var (
		job          *gitlab.Job
		lastRunning  *gitlab.Job
		firstPending *gitlab.Job
	)

	for _, j := range jobs {
		if j.Status == "running" {
			lastRunning = j
		}
		if j.Status == "pending" && firstPending == nil {
			firstPending = j
		}
		if j.Name == name {
			job = j
			// don't break because there may be a newer version of the job
		}
	}
	if job == nil {
		job = lastRunning
	}
	if job == nil {
		job = firstPending
	}
	if job == nil {
		job = jobs[len(jobs)-1]
	}
	return job, err
}

type jobSort struct {
	Jobs []*gitlab.Job
}

func (s jobSort) Len() int      { return len(s.Jobs) }
func (s jobSort) Swap(i, j int) { s.Jobs[i], s.Jobs[j] = s.Jobs[j], s.Jobs[i] }
func (s jobSort) Less(i, j int) bool {
	if (*s.Jobs[i].CreatedAt).Equal(*s.Jobs[j].CreatedAt) {
		return s.Jobs[i].ID < s.Jobs[j].ID
	}
	return (*s.Jobs[i].CreatedAt).Before(*s.Jobs[j].CreatedAt)
}

type bridgeSort struct {
	Bridges []*gitlab.Bridge
}

func (s bridgeSort) Len() int      { return len(s.Bridges) }
func (s bridgeSort) Swap(i, j int) { s.Bridges[i], s.Bridges[j] = s.Bridges[j], s.Bridges[i] }
func (s bridgeSort) Less(i, j int) bool {
	if (*s.Bridges[i].CreatedAt).Equal(*s.Bridges[j].CreatedAt) {
		return s.Bridges[i].ID < s.Bridges[j].ID
	}
	return (*s.Bridges[i].CreatedAt).Before(*s.Bridges[j].CreatedAt)
}

// PipelineJobsWithID returns a list of jobs in a pipeline for a id.
// The jobs are returned in the order in which they were created
func PipelineJobsWithID(client *gitlab.Client, pid any, ppid int64) ([]*gitlab.Job, []*gitlab.Bridge, error) {
	opts := &gitlab.ListJobsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 500,
		},
	}
	jobsList, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Job, *gitlab.Response, error) {
		return client.Jobs.ListPipelineJobs(pid, ppid, opts, p)
	})
	if err != nil {
		return nil, nil, err
	}
	// reset
	opts.Page = 0
	bridgesList, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Bridge, *gitlab.Response, error) {
		return client.Jobs.ListPipelineBridges(pid, ppid, opts, p)
	})
	if err != nil {
		return nil, nil, err
	}

	// ListPipelineJobs returns jobs sorted by ID in descending order instead of returning
	// them in the order they were created, so we restore the order using the createdAt
	sort.Sort(jobSort{Jobs: jobsList})
	sort.Sort(bridgeSort{Bridges: bridgesList})
	return jobsList, bridgesList, nil
}

// pipelineJobsWithSha returns a list of jobs in a pipeline for a given commit sha.
// The jobs are returned in the order in which they were created
func pipelineJobsWithSha(client *gitlab.Client, pid any, sha string) ([]*gitlab.Job, []*gitlab.Bridge, error) {
	pipelines, _, err := client.Pipelines.ListProjectPipelines(pid, &gitlab.ListProjectPipelinesOptions{SHA: new(sha), ListOptions: gitlab.ListOptions{PerPage: DefaultListLimit}})
	if err != nil {
		return nil, nil, err
	}
	if len(pipelines) == 0 {
		return nil, nil, nil
	}
	return PipelineJobsWithID(client, pid, pipelines[0].ID)
}
