package list

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	timelogapi "gitlab.com/gitlab-org/cli/internal/commands/timelog/api"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)

	group        string
	username     string
	startDate    time.Time
	endDate      time.Time
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List time logged on issues and merge requests.",
		Long: heredoc.Docf(`
			By default, reports on the current project. Use %[1]s--group%[1]s to report on a group and its subgroups, or %[1]s--repo%[1]s to report on another project. Entries are listed newest first, followed by the total time spent.

			Dates appear in UTC. Durations use time tracking units: 1 day is 8 hours, 1 week is 5 days, and 1 month is 4 weeks. Subtracted time appears as a negative value and reduces the total.

			Omits time logged on issues and merge requests you cannot view.
		`, "`"),
		Aliases: []string{"ls"},
		Example: heredoc.Doc(`
			# List all time logged in the current project
			glab timelog list

			# List the time you logged in a group and its subgroups since the start of the month
			glab timelog list --group gitlab-org --user @me --start-date 2026-10-01

			# List the time logged in another project on a single day
			glab timelog list -R gitlab-org/cli --start-date 2026-10-14 --end-date 2026-10-14

			# Total the seconds logged per user
			glab timelog list --output json --jq 'group_by(.user.username) | map({user: .[0].user.username, seconds: map(.timeSpent) | add})'`),
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

	cmdutils.EnableRepoOverride(cmd, f)
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "List time logged in a group and its subgroups.")
	fl.StringVar(&opts.username, "user", "", "List only the time logged by this `username`. Use @me for yourself.")
	fl.TimeVar(&opts.startDate, "start-date", time.Time{}, []string{time.DateOnly}, "List time logged on or after this date, in `YYYY-MM-DD` format.")
	fl.TimeVar(&opts.endDate, "end-date", time.Time{}, []string{time.DateOnly}, "List time logged on or before this date, in `YYYY-MM-DD` format.")

	cmd.MarkFlagsMutuallyExclusive("group", "repo")

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group
	return nil
}

func (o *options) validate() error {
	if !o.startDate.IsZero() && !o.endDate.IsZero() && o.endDate.Before(o.startDate) {
		return cmdutils.FlagError{Err: errors.New("invalid date range: --end-date is before --start-date")}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	listOpts := timelogapi.ListOptions{
		Group:     o.group,
		StartDate: o.startDate,
		EndDate:   o.endDate,
		Username:  strings.TrimPrefix(o.username, "@"),
	}
	if o.group == "" {
		repo, err := o.baseRepo()
		if err != nil {
			return err
		}
		listOpts.Project = repo.FullName()
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	if o.username == "@me" {
		user, _, err := client.Users.CurrentUser(gitlab.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("failed to get the current user: %w", err)
		}
		listOpts.Username = user.Username
	}

	var timelogs []timelogapi.Timelog
	for timelog, err := range timelogapi.ListTimelogs(ctx, client, listOpts) {
		if err != nil {
			return err
		}
		timelogs = append(timelogs, timelog)
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(timelogs)
	}

	scope := listOpts.Project
	if o.group != "" {
		scope = o.group
	}
	if len(timelogs) == 0 {
		o.io.LogInfof("No time logged in %s.\n", scope)
		return nil
	}

	var total int64
	table := tableprinter.NewTablePrinter()
	table.SetIsTTY(o.io.IsOutputTTY())
	table.AddRow("DATE", "USER", "TIME", "ITEM", "TITLE", "SUMMARY")
	for _, timelog := range timelogs {
		total += timelog.TimeSpent
		ref, item := reference(timelog, o.group != "")
		title, webURL := "", ""
		if item != nil {
			title, webURL = item.Title, item.WebURL
		}
		summary := timelog.Summary
		if summary == "" {
			summary = "-"
		}
		table.AddRow(
			timelog.SpentAt.UTC().Format(time.DateOnly),
			timelog.User.Username,
			utils.FormatTimeSpent(timelog.TimeSpent),
			o.io.Hyperlink(ref, webURL),
			title,
			summary,
		)
	}

	o.io.LogInfo(table.Render())
	o.io.LogInfof("Total: %s (%s)\n", utils.FormatTimeSpent(total), utils.Pluralize(len(timelogs), "time log"))
	return nil
}

// reference returns the short reference of the issue or merge request a
// timelog was logged on. Group reports span projects, so they prefix the
// project path.
func reference(timelog timelogapi.Timelog, withProject bool) (string, *timelogapi.Issuable) {
	prefix := ""
	if withProject {
		prefix = timelog.Project.FullPath
	}
	switch {
	case timelog.Issue != nil:
		return fmt.Sprintf("%s#%s", prefix, timelog.Issue.IID), timelog.Issue
	case timelog.MergeRequest != nil:
		return fmt.Sprintf("%s!%s", prefix, timelog.MergeRequest.IID), timelog.MergeRequest
	default:
		return "", nil
	}
}
