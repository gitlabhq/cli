package update

import (
	"context"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	a "gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/workitems/api"
	"gitlab.com/gitlab-org/cli/internal/commands/workitems/utils"
	"gitlab.com/gitlab-org/cli/internal/config"
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
	config       func() config.Config

	// Flags
	group       string
	iid         int64
	title       string
	description string
	assignee    []string
	milestone   string
	startDate   string
	dueDate     string
	weight      int64

	// internal state
	scope *api.ScopeInfo

	// Output
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
		config:       f.Config,
	}

	cmd := &cobra.Command{
		Use:   "update <iid> [flags]",
		Short: "Update work items in a project or group. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
		The command uses your repository context to detect scope automatically.
		
		Use %[1]s--group%[1]s to target a group or subgroup. %[1]s--group%[1]s and %[1]s--repo%[1]s are mutually exclusive.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
					# Update a work item in current project
					glab work-items update 42 --description "this issue tracks a new feature"
					
					# Update a work item in a group
					glab work-items update 40 --group MYGROUP --description "this epic tracks a new feature"

					# Read the description from a file
					glab work-items update 42 --description-file description.md

					# Read the description from standard input
					cat description.md | glab work-items update 42 --description-file -
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd.Context(), cmd, args); err != nil {
				return err
			}

			return opts.run(cmd)
		},
	}

	// enable -R flag for repo override
	cmdutils.EnableRepoOverride(cmd, f)

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	// Flags
	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "Update work items for a group or subgroup.")
	fl.StringVarP(&opts.title, "title", "t", "", "Update the title for the work item.")
	fl.StringVarP(&opts.description, "description", "d", "", "Update the description for the work item.")
	fl.Int64VarP(&opts.weight, "weight", "w", 0, "Update the weight value for the work item.")
	fl.StringSliceVarP(&opts.assignee, "assignee", "a", []string{}, "Update the work item assignee with the supplied GitLab usernames.")
	fl.StringVarP(&opts.milestone, "milestone", "m", "", "Update the work item milestone with the title or ID.")
	fl.StringVar(&opts.startDate, "startdate", "", "Update the start date for the work item.")
	fl.StringVar(&opts.dueDate, "duedate", "", "Update the due date for the work item.")
	cmdutils.AddDescriptionFileFlag(cmd, "work item")

	cmd.MarkFlagsMutuallyExclusive("group", "repo")

	return cmd
}

func (opts *options) complete(ctx context.Context, cmd *cobra.Command, args []string) error {
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

	if err := cmdutils.ResolveDescriptionFile(opts.io, cmd); err != nil {
		return err
	}

	if err := cmdutils.HandleDescriptionEditor(ctx, &opts.description, opts.io, opts.config, nil); err != nil {
		return err
	}

	scope, err := utils.DetectScope(opts.group, opts.baseRepo)
	if err != nil {
		return err
	}
	opts.scope = scope

	return nil
}

func (opts *options) run(cmd *cobra.Command) error {
	client, err := opts.gitlabClient()
	if err != nil {
		return fmt.Errorf("failed to get GitLab client: %w", err)
	}

	updateOpts := gitlab.UpdateWorkItemOptions{}

	if opts.startDate != "" {
		startDate, err := gitlab.ParseISOTime(opts.startDate)
		if err != nil {
			return cmdutils.FlagError{Err: fmt.Errorf("--startdate must be ISO 8601 (YYYY-MM-DD), got %q", opts.startDate)}
		}
		updateOpts.StartDate = new(startDate)
	}

	if opts.dueDate != "" {
		dueDate, err := gitlab.ParseISOTime(opts.dueDate)
		if err != nil {
			return cmdutils.FlagError{Err: fmt.Errorf("--duedate must be ISO 8601 (YYYY-MM-DD), got %q", opts.dueDate)}
		}
		updateOpts.DueDate = new(dueDate)
	}

	if opts.title != "" {
		updateOpts.Title = new(opts.title)
	}

	if cmd.Flags().Changed("description") {
		updateOpts.Description = new(opts.description)
	}

	if len(opts.assignee) != 0 {
		user, err := a.UsersByNames(client, opts.assignee)
		if err != nil {
			return cmdutils.FlagError{Err: fmt.Errorf("failed to find assignee: %w", err)}
		}

		assignees := make([]int64, 0)

		for _, i := range user {
			assignees = append(assignees, i.ID)
		}
		updateOpts.AssigneeIDs = assignees
	}

	if opts.milestone != "" {
		if milestoneID, err := strconv.ParseInt(opts.milestone, 10, 64); err == nil {
			updateOpts.MilestoneID = new(milestoneID)
		} else {
			if opts.scope.Type == "project" {

				l := &a.ListMilestonesOptions{
					Title: new(opts.milestone),
				}

				m, err := a.ListAllMilestones(client, opts.scope.Path, l)
				if err != nil || len(m) == 0 {
					return cmdutils.FlagError{Err: fmt.Errorf("failed to find project milestone by title")}
				}

				updateOpts.MilestoneID = new(m[0].ID)
			} else if opts.scope.Type == "group" {
				m, _, err := client.GroupMilestones.ListGroupMilestones(opts.scope.Path, &gitlab.ListGroupMilestonesOptions{Title: new(opts.milestone)})
				if err != nil || len(m) == 0 {
					return cmdutils.FlagError{Err: fmt.Errorf("failed to find group milestone by title")}
				}

				updateOpts.MilestoneID = new(m[0].ID)
			}
		}
	}

	if cmd.Flags().Changed("weight") {
		updateOpts.Weight = new(opts.weight)
	}

	wi, _, err := client.WorkItems.UpdateWorkItem(opts.scope.Path, opts.iid, &updateOpts)
	if err != nil {
		return err
	}

	switch opts.outputFormat {
	case "json":
		return opts.io.PrintJSON(wi)
	default:
		opts.io.LogInfo("- Updating work item in", opts.scope.Path)

		if opts.io.IsaTTY {
			opts.io.LogInfof("#%d %s\n%s\n", wi.IID, wi.Title, wi.WebURL)
		} else {
			opts.io.LogInfo(wi.WebURL)
		}
	}

	return nil
}
