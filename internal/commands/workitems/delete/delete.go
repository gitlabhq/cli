package delete

import (
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/workitems/api"
	"gitlab.com/gitlab-org/cli/internal/commands/workitems/utils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	// Dependencies
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)

	// Flags
	group string
	iid   int64

	// Internal state
	scope *api.ScopeInfo

	// Output
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "delete <iid>",
		Short: "Delete a work item in a project or group. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Delete a work item by its internal ID (IID). This action cannot be undone.

			The command behavior depends on context:

			- By default, deletes from the current repository's project.
			- With %[1]s--group%[1]s, deletes from the specified group.
			- With %[1]s--repo%[1]s, deletes from the specified project.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
		# Delete a work item by IID from the current project
		glab work-items delete 42

		# Delete a group work item
		glab work-items delete 42 --group my-group
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			return opts.run()
		},
	}

	// enable -R flag for repo override
	cmdutils.EnableRepoOverride(cmd, f)

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	// Flags
	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "Delete a work items from a group or subgroup.")

	cmd.MarkFlagsMutuallyExclusive("group", "repo")

	return cmd
}

func (opts *options) complete(cmd *cobra.Command, args []string) error {
	iid, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid work item ID: %w", err)
	}
	opts.iid = iid

	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	opts.group = group

	scope, err := utils.DetectScope(opts.group, opts.baseRepo)
	if err != nil {
		return err
	}
	opts.scope = scope
	return nil
}

func (opts *options) run() error {
	client, err := opts.gitlabClient()
	if err != nil {
		return fmt.Errorf("failed to get GitLab client: %w", err)
	}

	opts.io.LogInfo("- Deleting work item in", opts.scope.Path)

	_, err = client.WorkItems.DeleteWorkItem(opts.scope.Path, opts.iid)
	if err != nil {
		return err
	}

	if opts.outputFormat == "json" {
		err := opts.io.PrintJSON(struct {
			DeletedWorkItemID int64 `json:"deleted_work_item_id"`
		}{DeletedWorkItemID: opts.iid})
		if err != nil {
			return err
		}
	} else if opts.io.IsaTTY {
		opts.io.LogInfof("Successfully deleted %d\n", opts.iid)
	} else {
		opts.io.LogInfo(opts.iid)
	}

	return nil
}
