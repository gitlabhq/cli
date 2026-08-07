package list

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var getSchedules = func(client *gitlab.Client, l *gitlab.ListPipelineSchedulesOptions, repo string) ([]*gitlab.PipelineSchedule, error) {
	schedules, _, err := client.PipelineSchedules.ListPipelineSchedules(repo, l)
	return schedules, err
}

type options struct {
	outputFormat string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}

	scheduleListCmd := &cobra.Command{
		Use:   "list [flags]",
		Short: `List pipeline schedules in a project.`,
		Long: heredoc.Docf(`
		List CI/CD pipeline schedules in a project. By default, schedules
		are listed for the current project. Use %[1]s--repo%[1]s to target another
		project.

		Use %[1]s--output json%[1]s to format the result as JSON for use with other
		tools.
		`, "`"),
		Example: heredoc.Doc(`
			# List schedules for the current project
			glab schedule list

			# List schedules in another project
			glab schedule list -R owner/repo

			# List schedules as JSON
			glab schedule list --output json
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.gitlabClient()
			if err != nil {
				return err
			}

			repo, err := opts.baseRepo()
			if err != nil {
				return err
			}

			l := &gitlab.ListPipelineSchedulesOptions{}
			page, _ := cmd.Flags().GetInt("page")
			l.Page = int64(page)
			perPage, _ := cmd.Flags().GetInt("per-page")
			l.PerPage = int64(perPage)

			schedules, err := getSchedules(client, l, repo.FullName())
			if err != nil {
				return err
			}

			if opts.outputFormat == "json" {
				return opts.io.PrintJSON(schedules)
			}

			title := utils.NewListTitle("schedule")
			title.RepoName = repo.FullName()
			title.Page = int(l.Page)
			title.CurrentPageTotal = len(schedules)

			opts.io.LogInfof("%s\n%s\n", title.Describe(), ciutils.DisplaySchedules(opts.io, schedules, repo.FullName()))
			return nil
		},
	}
	scheduleListCmd.Flags().IntP("page", "p", 1, "Page number.")
	scheduleListCmd.Flags().IntP("per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(scheduleListCmd, opts.io, &opts.outputFormat)

	return scheduleListCmd
}
