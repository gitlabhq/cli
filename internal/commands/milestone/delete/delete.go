package delete

import (
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	projectID   string
	groupID     string
	milestoneID int64
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "delete <id> [flags]",
		Short: "Delete a milestone from a project or group.",
		Long: heredoc.Docf(`
		Delete a milestone, identified by its numeric ID, from a project or
		group. The milestone is removed; issues, merge requests, and epics
		that referenced it are no longer associated with it.

		By default, the milestone is deleted from the current project. Use
		%[1]s--project%[1]s to target a different project, or %[1]s--group%[1]s to delete a
		group-level milestone. %[1]s--project%[1]s and %[1]s--group%[1]s are mutually exclusive.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete a milestone from the current project
			glab milestone delete 123

			# Delete a milestone from a different project
			glab milestone delete 123 --project owner/project

			# Delete a group milestone
			glab milestone delete 123 --group example-group
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			milestoneIDInt, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}
			opts.milestoneID = int64(milestoneIDInt)

			return opts.run()
		},
	}

	cmd.Flags().StringVar(&opts.projectID, "project", "", "The ID or URL-encoded path of the project.")
	cmd.Flags().StringVar(&opts.groupID, "group", "", "The ID or URL-encoded path of the group.")

	return cmd
}

func (o *options) run() error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()

	switch {
	case o.projectID != "":
		_, err := client.Milestones.DeleteMilestone(o.projectID, o.milestoneID)
		if err != nil {
			return err
		}

		o.io.LogInfo(fmt.Sprintf("Deleted project milestone with ID %d.", o.milestoneID))
	case o.groupID != "":
		_, err := client.GroupMilestones.DeleteGroupMilestone(o.groupID, o.milestoneID)
		if err != nil {
			return err
		}

		o.io.LogInfo(fmt.Sprintf("Deleted group milestone with ID %d.", o.milestoneID))
	default:
		repo, err := o.baseRepo()
		if err != nil {
			return err
		}
		_, err = client.Milestones.DeleteMilestone(repo.FullName(), o.milestoneID)
		if err != nil {
			return err
		}

		o.io.LogInfo(fmt.Sprintf("Deleted project milestone with ID %d.", o.milestoneID))
	}
	return nil
}
