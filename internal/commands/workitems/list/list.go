package list

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

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
	dates        map[workitemsapi.DateField]*dateFilterValue
}

var dateFlags = []struct {
	name  string
	field workitemsapi.DateField
	usage string
}{
	{"created-after", workitemsapi.CreatedAfter, "Show work items created on or after <date>."},
	{"created-before", workitemsapi.CreatedBefore, "Show work items created before <date>."},
	{"updated-after", workitemsapi.UpdatedAfter, "Show work items updated on or after <date>."},
	{"updated-before", workitemsapi.UpdatedBefore, "Show work items updated before <date>."},
	{"closed-after", workitemsapi.ClosedAfter, "Show work items closed on or after <date>. Implies --state closed unless --state is set."},
	{"closed-before", workitemsapi.ClosedBefore, "Show work items closed before <date>. Implies --state closed unless --state is set."},
	{"due-after", workitemsapi.DueAfter, "Show work items due on or after <date>."},
	{"due-before", workitemsapi.DueBefore, "Show work items due before <date>."},
}

// dateFilterValue keeps the raw input so the next page command repeats a
// relative date as given, instead of the date it resolved to.
type dateFilterValue struct {
	raw    string
	filter workitemsapi.DateFilter
}

func (d *dateFilterValue) Type() string   { return "date" }
func (d *dateFilterValue) String() string { return d.raw }

func (d *dateFilterValue) Set(s string) error {
	filter, err := workitemsapi.ParseDateFilter(s)
	if err != nil {
		return err
	}
	d.raw, d.filter = s, filter
	return nil
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
		dates:        make(map[workitemsapi.DateField]*dateFilterValue, len(dateFlags)),
	}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List work items in a project or group. (EXPERIMENTAL)",
		Long: heredoc.Doc(`List work items in a project or group.

Automatically detects scope from repository context. Use --group flag
for group-level work items or -R to specify a different project.

Date filters accept:

- An absolute date (YYYY-MM-DD). A date without a time is read in the system time zone of the GitLab server.
- An RFC 3339 timestamp. Use one with an offset, such as 2026-01-01T00:00:00+02:00, for an exact point in time.
- A relative date: a number followed by d (days), w (weeks), m (months), or y (years). Negative numbers are in the past, positive numbers are in the future, and 0d is today. Relative dates resolve to the start of the day in the time zone of the GitLab instance. They stay relative, so the same command keeps returning the last 7 days, for example.

Date filters require GitLab 19.4 or later. Relative dates require GitLab 19.6 or later.
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
				glab work-items list --type issue -R gitlab-org/cli

				# List work items created in the last 7 days
				glab work-items list --created-after -7d -g gitlab-org

				# List open work items due in the next 2 weeks
				glab work-items list --due-after 0d --due-before 2w -g gitlab-org

				# List work items closed in a fixed date range
				glab work-items list --state closed --closed-after 2026-01-01 --closed-before 2026-04-01 -g gitlab-org`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
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
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	// Flags
	fl := cmd.Flags()
	fl.StringP("group", "g", "", "List work items for a group or subgroup.")
	fl.StringSliceVarP(&opts.types, "type", "t", []string{}, "Filter by work item type (epic, issue, task, etc.) Multiple types can be comma-separated or specified by repeating the flag.")

	fl.StringVar(&opts.state, "state", "opened", "Filter by state: opened, closed, all.")
	fl.StringVar(&opts.after, "after", "", "Fetch items after this cursor (for pagination)")
	fl.Int64VarP(&opts.perPage, "per-page", "P", 20, "Number of items to list per page (max 100)")

	for _, df := range dateFlags {
		v := &dateFilterValue{}
		opts.dates[df.field] = v
		fl.Var(v, df.name, df.usage+" Use YYYY-MM-DD, an RFC 3339 timestamp, or a relative date such as -7d, 2w, -1m, or 1y.")
	}

	return cmd
}

func (opts *options) complete(cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	opts.group = group

	// Reopening a work item clears its closed date, so open items never match.
	if opts.hasClosedFilter() && !cmd.Flags().Changed("state") {
		opts.state = "closed"
	}
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

	if opts.hasClosedFilter() && opts.state == "opened" {
		return cmdutils.FlagError{
			Err: fmt.Errorf("--closed-after and --closed-before need --state closed or all"),
		}
	}

	return nil
}

func (opts *options) hasClosedFilter() bool {
	return opts.dates[workitemsapi.ClosedAfter].raw != "" || opts.dates[workitemsapi.ClosedBefore].raw != ""
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
	workItems, pageInfo, err := workitemsapi.FetchWorkItems(ctx, client, scope, workitemsapi.ListOptions{
		Types:   opts.types,
		State:   opts.state,
		After:   opts.after,
		PerPage: opts.perPage,
		Dates:   opts.dateFilters(),
	})
	if err != nil {
		return err
	}

	switch opts.outputFormat {
	case "json":
		return outputJSON(opts.io, workItems, pageInfo, opts.perPage)
	case "text":
		// display a table
		if len(workItems) == 0 {
			opts.io.LogInfof("No work items found in %s\n", scope.Path)
			return nil
		}

		table := utils.DisplayWorkItemList(opts.io, workItems)
		opts.io.LogInfof("%s", table)

		if pageInfo != nil && pageInfo.HasNextPage {
			opts.io.LogInfof("\nNext page: %s\n", buildNextPageCommand(opts, pageInfo.EndCursor))
		}
		return nil
	default:
		return fmt.Errorf("unsupported output format: %s", opts.outputFormat)
	}
}

func (opts *options) dateFilters() map[workitemsapi.DateField]workitemsapi.DateFilter {
	filters := make(map[workitemsapi.DateField]workitemsapi.DateFilter)
	for field, v := range opts.dates {
		if v.raw != "" {
			filters[field] = v.filter
		}
	}
	return filters
}

// buildNextPageCommand constructs the next page command string with preserved flags.
func buildNextPageCommand(opts *options, cursor string) string {
	var b strings.Builder

	b.WriteString("glab work-items list")

	// preserve group flag
	if opts.group != "" {
		fmt.Fprintf(&b, " -g %s", opts.group) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	}

	// preserve type filters
	if len(opts.types) > 0 {
		fmt.Fprintf(&b, " --type %s", strings.Join(opts.types, ",")) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	}

	// preserve state filter (only if non-default)
	if opts.state != "opened" {
		fmt.Fprintf(&b, " --state %s", opts.state) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	}

	for _, df := range dateFlags {
		if v := opts.dates[df.field]; v.raw != "" {
			fmt.Fprintf(&b, " --%s %s", df.name, v.raw) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
		}
	}

	// preserve per-page (only if non-default)
	if opts.perPage != 20 {
		fmt.Fprintf(&b, " --per-page %d", opts.perPage) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	}

	// preserve output format (only if non-default)
	if opts.outputFormat != "text" {
		fmt.Fprintf(&b, " --output %s", opts.outputFormat) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	}

	// add the cursor
	fmt.Fprintf(&b, " --after %q", cursor) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr

	return b.String()
}

func outputJSON(io *iostreams.IOStreams, workItems []workitemsapi.WorkItem, pageInfo *workitemsapi.PageInfo, perPage int64) error {
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

	return io.PrintJSON(response)
}
