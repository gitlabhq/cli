package api

import (
	"sort"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

func PlayOrRetryJobs(client *gitlab.Client, pid any, jobID int64, status string) (*gitlab.Job, error) {
	switch status {
	case "pending", "running":
		return nil, nil
	case "manual":
		j, _, err := client.Jobs.PlayJob(pid, jobID, &gitlab.PlayJobOptions{})
		if err != nil {
			return nil, err
		}
		return j, nil
	default:

		j, _, err := client.Jobs.RetryJob(pid, jobID)
		if err != nil {
			return nil, err
		}

		return j, nil
	}
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
