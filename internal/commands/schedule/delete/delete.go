package delete

import (
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	scheduleID int64

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	scheduleDeleteCmd := &cobra.Command{
		Use:   "delete <id> [flags]",
		Short: `Delete a pipeline schedule by ID.`,
		Long: heredoc.Docf(`
		Delete a CI/CD pipeline schedule, identified by its numeric ID. The
		schedule is removed from the project; pipelines previously triggered
		by it are not affected.

		By default, the schedule is deleted from the current project. Use
		%[1]s--repo%[1]s to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete the schedule with ID 10
			glab schedule delete 10

			# Delete a schedule in another project
			glab schedule delete 10 -R owner/repo
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
	return scheduleDeleteCmd
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

	_, err = client.PipelineSchedules.DeletePipelineSchedule(repo.FullName(), o.scheduleID)
	if err != nil {
		return err
	}
	o.io.LogInfo("Deleted schedule with ID", o.scheduleID)

	return nil
}
