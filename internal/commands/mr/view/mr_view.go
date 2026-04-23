package view

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	showComments   bool
	showSystemLogs bool
	showResolved   bool
	showUnresolved bool
	openInBrowser  bool
	outputFormat   string

	commentPageNujmber int
	commentLimit       int

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	config       func() config.Config
}

type MRWithDiscussions struct {
	*gitlab.MergeRequest
	Discussions []*gitlab.Discussion
}

func NewCmdView(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		config:       f.Config,
	}
	mrViewCmd := &cobra.Command{
		Use:     "view [<id | branch>]",
		Short:   `Display the title, body, and other information about a merge request.`,
		Long:    ``,
		Aliases: []string{"show"},
		Args:    cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd.Context(), f, args)
		},
	}

	mrViewCmd.Flags().BoolVarP(&opts.showComments, "comments", "c", false, "Show merge request comments and activities.")
	mrViewCmd.Flags().BoolVarP(&opts.showSystemLogs, "system-logs", "s", false, "Show system activities and logs.")
	mrViewCmd.Flags().BoolVar(&opts.showResolved, "resolved", false, "Show only resolved discussions (implies --comments).")
	mrViewCmd.Flags().BoolVar(&opts.showUnresolved, "unresolved", false, "Show only unresolved discussions (implies --comments).")
	cmdutils.EnableJSONOutput(mrViewCmd, &opts.outputFormat)
	mrViewCmd.Flags().BoolVarP(&opts.openInBrowser, "web", "w", false, "Open merge request in a browser. Uses default browser or browser specified in BROWSER variable.")
	mrViewCmd.Flags().IntVarP(&opts.commentPageNujmber, "page", "p", 0, "Page number.")
	mrViewCmd.Flags().IntVarP(&opts.commentLimit, "per-page", "P", 20, "Number of items to list per page.")

	return mrViewCmd
}

func (o *options) run(ctx context.Context, f cmdutils.Factory, args []string) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	mr, baseRepo, err := mrutils.MRFromArgsWithOpts(ctx, f, args, &gitlab.GetMergeRequestsOptions{
		IncludeDivergedCommitsCount: new(true),
		RenderHTML:                  new(true),
		IncludeRebaseInProgress:     new(true),
	}, "any")
	if err != nil {
		return err
	}

	// Optional: check for approval state of the MR (if the project supports it). In the event of a failure
	// for this step, move forward assuming MR approvals are not supported. See below.
	//
	// NOTE: the API documentation says that project details have `approvals_before_merge` for GitLab Premium
	// https://docs.gitlab.com/api/projects/#get-a-single-project. Unfortunately, the API client used
	// does not provide the necessary ability to determine if this value was present or not in the response JSON
	// since Project.ApprovalsBeforeMerge is a non-pointer type. Because of this, this step will either succeed
	// and show approval state or it will fail silently
	mrApprovals, _, err := client.MergeRequestApprovals.GetApprovalState(baseRepo.FullName(), mr.IID) //nolint:ineffassign,staticcheck

	cfg := o.config()

	if o.openInBrowser { // open in browser if --web flag is specified
		if o.io.IsOutputTTY() {
			fmt.Fprintf(o.io.StdErr, "Opening %s in your browser.\n", utils.DisplayURL(mr.WebURL))
		}

		browser, _ := cfg.Get(baseRepo.RepoHost(), "browser")
		return utils.OpenInBrowser(mr.WebURL, browser)
	}

	discussions := []*gitlab.Discussion{}

	// --resolved and --unresolved imply --comments
	if o.showResolved || o.showUnresolved {
		o.showComments = true
	}

	if o.showComments {
		l := &gitlab.ListMergeRequestDiscussionsOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.commentPageNujmber),
				PerPage: int64(o.commentLimit),
				Sort:    "asc",
			},
		}

		discussions, err = mrutils.ListAllDiscussions(ctx, client, baseRepo.FullName(), mr.IID, l)
		if err != nil {
			return err
		}

		// Filter discussions based on resolution status
		if o.showResolved || o.showUnresolved {
			var state string
			switch {
			case o.showResolved && o.showUnresolved:
				state = "resolvable"
			case o.showResolved:
				state = "resolved"
			default:
				state = "unresolved"
			}
			discussions = mrutils.FilterDiscussions(discussions, mrutils.FilterOpts{State: state})
		}
	}

	glamourStyle, _ := cfg.Get(baseRepo.RepoHost(), "glamour_style")
	o.io.ResolveBackgroundColor(glamourStyle)
	if err := o.io.StartPager(); err != nil {
		return err
	}
	defer o.io.StopPager()

	switch {
	case o.outputFormat == "json":
		printJSONMR(o, mr, discussions)
	case o.io.IsOutputTTY():
		printTTYMRPreview(o, mr, mrApprovals, discussions)
	default:
		printRawMRPreview(o, mr, discussions)
	}
	return nil
}

func labelsList(mr *gitlab.MergeRequest) string {
	return strings.Join(mr.Labels, ", ")
}

func assigneesList(mr *gitlab.MergeRequest) string {
	assignees := utils.Map(mr.Assignees, func(a *gitlab.BasicUser) string {
		return a.Username
	})

	return strings.Join(assignees, ", ")
}

func reviewersList(mr *gitlab.MergeRequest) string {
	reviewers := utils.Map(mr.Reviewers, func(r *gitlab.BasicUser) string {
		return r.Username
	})

	return strings.Join(reviewers, ", ")
}

func mrState(c *iostreams.ColorPalette, mr *gitlab.MergeRequest) string {
	switch mr.State {
	case "opened":
		return c.Green("open")
	case "merged":
		return c.Blue(mr.State)
	default:
		return c.Red(mr.State)
	}
}

func printTTYMRPreview(opts *options, mr *gitlab.MergeRequest, mrApprovals *gitlab.MergeRequestApprovalState, discussions []*gitlab.Discussion) {
	c := opts.io.Color()
	out := opts.io.StdOut
	mrTimeAgo := utils.TimeToPrettyTimeAgo(*mr.CreatedAt)
	// Header
	fmt.Fprint(out, mrState(c, mr))
	fmt.Fprintf(out, c.Gray(" • opened by @%s %s\n"), mr.Author.Username, mrTimeAgo)
	fmt.Fprint(out, mr.Title)
	fmt.Fprintf(out, c.Gray(" !%d"), mr.IID)
	fmt.Fprintln(out)

	// Description
	if mr.Description != "" {
		mr.Description, _ = utils.RenderMarkdown(mr.Description, opts.io.BackgroundColor())
		fmt.Fprintln(out, mr.Description)
	}

	fmt.Fprintf(out, c.Gray("\n%d upvotes • %d downvotes • %d comments\n"), mr.Upvotes, mr.Downvotes, mr.UserNotesCount)

	// Meta information
	if labels := labelsList(mr); labels != "" {
		fmt.Fprint(out, c.Bold("Labels: "))
		fmt.Fprintln(out, labels)
	}
	if assignees := assigneesList(mr); assignees != "" {
		fmt.Fprint(out, c.Bold("Assignees: "))
		fmt.Fprintln(out, assignees)
	}
	if reviewers := reviewersList(mr); reviewers != "" {
		fmt.Fprint(out, c.Bold("Reviewers: "))
		fmt.Fprintln(out, reviewers)
	}
	if mr.Milestone != nil {
		fmt.Fprint(out, c.Bold("Milestone: "))
		fmt.Fprintln(out, mr.Milestone.Title)
	}
	if mr.State == "closed" {
		if mr.ClosedBy != nil {
			fmt.Fprintf(out, "Closed by: %s %s\n", mr.ClosedBy.Username, mrTimeAgo)
		} else {
			fmt.Fprintf(out, "Closed %s\n", mrTimeAgo)
		}
	}
	if mr.Pipeline != nil {
		fmt.Fprint(out, c.Bold("Pipeline status: "))
		var status string
		switch s := mr.Pipeline.Status; s {
		case "failed":
			status = c.Red(s)
		case "success":
			status = c.Green(s)
		default:
			status = c.Gray(s)
		}
		fmt.Fprintf(out, "%s (View pipeline with `%s`)\n", status, c.Bold("glab ci view "+mr.SourceBranch))

		if mr.MergeWhenPipelineSucceeds && mr.Pipeline.Status != "success" {
			fmt.Fprintf(out, "%s Requires pipeline to succeed before merging.\n", c.WarnIcon())
		}
	}
	if mrApprovals != nil {
		fmt.Fprintln(out, c.Bold("Approvals status:"))
		mrutils.PrintMRApprovalState(opts.io, mrApprovals)
	}
	fmt.Fprintf(out, "%s This merge request has %s changes.\n", c.GreenCheck(), c.Yellow(mr.ChangesCount))
	if mr.State == "merged" && mr.MergedBy != nil { //nolint:staticcheck
		fmt.Fprintf(out, "%s The changes were merged into %s by %s %s.\n", c.GreenCheck(), mr.TargetBranch, mr.MergedBy.Name, utils.TimeToPrettyTimeAgo(*mr.MergedAt)) //nolint:staticcheck
	}

	if mr.HasConflicts {
		fmt.Fprintf(out, c.Red("%s This branch has conflicts that must be resolved.\n"), c.FailedIcon())
	}

	// Comments
	if opts.showComments {
		fmt.Fprintln(out, heredoc.Doc(`
			--------------------------------------------
			Discussions
			--------------------------------------------
			`))
		if len(discussions) > 0 {
			mrutils.PrintDiscussionsTTY(out, opts.io, discussions, opts.showSystemLogs)
		} else {
			// Provide specific message based on filter flags
			if opts.showResolved && !opts.showUnresolved {
				fmt.Fprintln(out, "This merge request has no resolved threads.")
			} else if opts.showUnresolved && !opts.showResolved {
				fmt.Fprintln(out, "This merge request has no unresolved threads.")
			} else {
				fmt.Fprintln(out, "This merge request has no comments.")
			}
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, c.Gray("View this merge request on GitLab: %s\n"), mr.WebURL)
}

func printRawMRPreview(opts *options, mr *gitlab.MergeRequest, discussions []*gitlab.Discussion) {
	fmt.Fprint(opts.io.StdOut, rawMRPreview(opts, mr, discussions))
}

func rawMRPreview(opts *options, mr *gitlab.MergeRequest, discussions []*gitlab.Discussion) string {
	var out strings.Builder

	assignees := assigneesList(mr)
	reviewers := reviewersList(mr)
	labels := labelsList(mr)

	fmt.Fprintf(&out, "title:\t%s\n", mr.Title)
	fmt.Fprintf(&out, "state:\t%s\n", mrState(opts.io.Color(), mr))
	fmt.Fprintf(&out, "author:\t%s\n", mr.Author.Username)
	fmt.Fprintf(&out, "labels:\t%s\n", labels)
	fmt.Fprintf(&out, "assignees:\t%s\n", assignees)
	fmt.Fprintf(&out, "reviewers:\t%s\n", reviewers)
	fmt.Fprintf(&out, "comments:\t%d\n", mr.UserNotesCount)
	if mr.Milestone != nil {
		fmt.Fprintf(&out, "milestone:\t%s\n", mr.Milestone.Title)
	}
	fmt.Fprintf(&out, "number:\t%d\n", mr.IID)
	fmt.Fprintf(&out, "url:\t%s\n", mr.WebURL)
	fmt.Fprintf(&out, "--\n")
	fmt.Fprintf(&out, "%s\n", mr.Description)

	if opts.showComments {
		mrutils.PrintDiscussionsRaw(&out, discussions, opts.showSystemLogs)
	}

	return out.String()
}

func printJSONMR(opts *options, mr *gitlab.MergeRequest, discussions []*gitlab.Discussion) {
	if opts.showComments {
		extendedMR := MRWithDiscussions{mr, discussions}
		mrJSON, _ := json.Marshal(extendedMR)
		fmt.Fprintln(opts.io.StdOut, string(mrJSON))
	} else {
		mrJSON, _ := json.Marshal(mr)
		fmt.Fprintln(opts.io.StdOut, string(mrJSON))
	}
}
