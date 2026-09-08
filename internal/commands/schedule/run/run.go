package run

import (
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var runSchedule = func(client *gitlab.Client, repo string, schedule int64, opts ...gitlab.RequestOptionFunc) error {
	_, err := client.PipelineSchedules.RunPipelineSchedule(repo, schedule, opts...)
	if err != nil {
		return fmt.Errorf("running scheduled pipeline status: %w", err)
	}

	return nil
}

type options struct {
	scheduleID int64

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdRun(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	scheduleRunCmd := &cobra.Command{
		Use:   "run <id>",
		Short: `Trigger a pipeline schedule to run immediately.`,
		Long: heredoc.Docf(`
		Trigger a CI/CD pipeline schedule, identified by its numeric ID, to
		run immediately. The schedule's normal recurrence is not affected;
		this is a one-time, on-demand run in addition to the configured cron.

		By default, the schedule is run in the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Run the schedule with ID 1 immediately
			glab schedule run 1

			# Run a schedule in another project
			glab schedule run 1 -R owner/repo
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run()
		},
	}
	return scheduleRunCmd
}

func (o *options) complete(args []string) error {
	id, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return err
	}
	o.scheduleID = int64(id)

	return nil
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	err = runSchedule(client, repo.FullName(), o.scheduleID)
	if err != nil {
		return err
	}

	o.io.LogInfo("Started schedule with ID", o.scheduleID)

	return nil
}
