package list

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	workitemsapi "gitlab.com/gitlab-org/cli/internal/commands/workitems/api"
	"gitlab.com/gitlab-org/cli/internal/commands/workitems/utils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	// Dependencies (for testability)
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)

	// Flags
	group        string
	types        []string
	outputFormat string
	state        string
	after        string
	perPage      int64
}

// jsonPaginationInfo contains pagination metrics for JSON responses
type jsonPaginationInfo struct {
	HasNext    bool   `json:"has_next"`
	NextCursor string `json:"next_cursor,omitempty"`
	PerPage    int64  `json:"per_page"`
}

// jsonListResponse wraps work items with pagination metadata
type jsonListResponse struct {
	Data       []workitemsapi.WorkItem `json:"data"`
	Pagination jsonPaginationInfo      `json:"pagination"`
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List work items in a project or group. (EXPERIMENTAL)",
		Long: heredoc.Doc(`List work items in a project or group.

Automatically detects scope from repository context. Use --group flag
for group-level work items or -R to specify a different project.
`) + text.ExperimentalString,
		Aliases: []string{"ls"},
		Example: heredoc.Doc(`
				# List first 20 open work items in current project
				glab work-items list

				# List open epics in a group (default: 20 items)
				glab work-items list --type epic -g gitlab-org

				# List first 50 open work items
				glab work-items list --per-page 50 -g gitlab-org

				# Get next page using cursor from previous output
				glab work-items list --after "eyJpZCI6OTk5OX0" -g gitlab-org

				# List closed work items
				glab work-items list --state closed -g gitlab-org

				# List all work items regardless of state
				glab work-items list --state all -g gitlab-org

				# JSON output with pagination metadata
				glab work-items list --output json -g gitlab-org

				# List issues in a specific project
				glab work-items list --type issue -R gitlab-org/cli`),
		Args: cobra.ExactArgs(0),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	// Enable -R flag for repo override
	cmdutils.EnableRepoOverride(cmd, f)
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat)

	// Flags
	cmd.Flags().StringP("group", "g", "", "List work items for a group or subgroup.")
	cmd.Flags().StringSliceVarP(&opts.types, "type", "t", []string{}, "Filter by work item type (epic, issue, task, etc.) Multiple types can be comma-separated or specified by repeating the flag.")

	cmd.Flags().StringVar(&opts.state, "state", "opened", "Filter by state: opened, closed, all.")
	cmd.Flags().StringVar(&opts.after, "after", "", "Fetch items after this cursor (for pagination)")
	cmd.Flags().Int64VarP(&opts.perPage, "per-page", "P", 20, "Number of items to list per page (max 100)")

	return cmd
}

func (opts *options) complete(cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	opts.group = group
	return nil
}

func (opts *options) validate() error {
	if err := utils.ValidateTypes(opts.types); err != nil {
		return cmdutils.FlagError{Err: err}
	}

	if opts.perPage < 1 || opts.perPage > 100 {
		return cmdutils.FlagError{
			Err: fmt.Errorf("--per-page must be between 1 and 100"),
		}
	}

	validStates := []string{"opened", "closed", "all"}
	if !slices.Contains(validStates, opts.state) {
		return cmdutils.FlagError{
			Err: fmt.Errorf("--state must be one of: opened, closed, all"),
		}
	}

	return nil
}

func (opts *options) run(ctx context.Context) error {
	scope, err := utils.DetectScope(opts.group, opts.baseRepo)
	if err != nil {
		return err
	}

	client, err := opts.gitlabClient()
	if err != nil {
		return fmt.Errorf("failed to get GitLab client: %w", err)
	}

	// fetch work items with state filtering and pagination
	workItems, pageInfo, err := workitemsapi.FetchWorkItems(ctx, client, scope, opts.types, opts.state, opts.after, opts.perPage)
	if err != nil {
		return err
	}

	switch opts.outputFormat {
	case "json":
		return outputJSON(opts.io.StdOut, workItems, pageInfo, opts.perPage)
	case "text":
		// display a table
		if len(workItems) == 0 {
			fmt.Fprintf(opts.io.StdOut, "No work items found in %s\n", scope.Path)
			return nil
		}

		table := utils.DisplayWorkItemList(opts.io, workItems)
		fmt.Fprint(opts.io.StdOut, table)

		if pageInfo != nil && pageInfo.HasNextPage {
			fmt.Fprintf(opts.io.StdOut, "\nNext page: %s\n", buildNextPageCommand(opts, pageInfo.EndCursor))
		}
		return nil
	default:
		return fmt.Errorf("unsupported output format: %s", opts.outputFormat)
	}
}

// buildNextPageCommand constructs the next page command string with preserved flags.
func buildNextPageCommand(opts *options, cursor string) string {
	var b strings.Builder

	b.WriteString("glab work-items list")

	// preserve group flag
	if opts.group != "" {
		fmt.Fprintf(&b, " -g %s", opts.group)
	}

	// preserve type filters
	if len(opts.types) > 0 {
		fmt.Fprintf(&b, " --type %s", strings.Join(opts.types, ","))
	}

	// preserve state filter (only if non-default)
	if opts.state != "opened" {
		fmt.Fprintf(&b, " --state %s", opts.state)
	}

	// preserve per-page (only if non-default)
	if opts.perPage != 20 {
		fmt.Fprintf(&b, " --per-page %d", opts.perPage)
	}

	// preserve output format (only if non-default)
	if opts.outputFormat != "text" {
		fmt.Fprintf(&b, " --output %s", opts.outputFormat)
	}

	// add the cursor
	fmt.Fprintf(&b, " --after %q", cursor)

	return b.String()
}

func outputJSON(w io.Writer, workItems []workitemsapi.WorkItem, pageInfo *workitemsapi.PageInfo, perPage int64) error {
	response := jsonListResponse{
		Data: workItems,
		Pagination: jsonPaginationInfo{
			HasNext: false,
			PerPage: perPage,
		},
	}

	if pageInfo != nil && pageInfo.HasNextPage {
		response.Pagination.HasNext = true
		response.Pagination.NextCursor = pageInfo.EndCursor
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(response)
}
