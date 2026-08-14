package view

import (
	"context"
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
		Use:   "view [<id | branch>]",
		Short: `Display the title, body, and other information about a merge request.`,
		Long: heredoc.Docf(`
			You can use a branch name or ID. Use %[1]s--web%[1]s to open in a browser.
		`, "`"),
		Example: heredoc.Doc(`
			glab mr view 123
			glab mr view branch-name
			glab mr view 123 --comments
			glab mr view 123 --web`),
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
	cmdutils.EnableJSONOutput(mrViewCmd, opts.io, &opts.outputFormat)
	mrViewCmd.Flags().BoolVarP(&opts.openInBrowser, "web", "w", false, "Open merge request in a browser. Uses default browser or browser specified in BROWSER variable.")
	mrViewCmd.Flags().IntVarP(&opts.commentPageNujmber, "page", "p", 0, "Page number.")
	mrViewCmd.Flags().IntVarP(&opts.commentLimit, "per-page", "P", 20, "Number of items to list per page.")

	return mrViewCmd
}

func (o *options) run(ctx context.Context, f cmdutils.Factory, args []string) error {
	// Opening in a browser only needs the web URL, so it skips the extra
	// merge request and approval state requests the terminal output needs.
	if o.openInBrowser {
		return o.openMRInBrowser(ctx, f, args)
	}

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
		return printJSONMR(o, mr, discussions)
	case o.io.IsOutputTTY():
		printTTYMRPreview(o, mr, mrApprovals, discussions)
	default:
		printRawMRPreview(o, mr, discussions)
	}
	return nil
}

func (o *options) openMRInBrowser(ctx context.Context, f cmdutils.Factory, args []string) error {
	webURL, baseRepo, err := mrutils.MRWebURLFromArgs(ctx, f, args, "any")
	if err != nil {
		return err
	}

	if o.io.IsOutputTTY() {
		o.io.LogErrorf("Opening %s in your browser.\n", utils.DisplayURL(webURL))
	}

	browser, _ := o.config().Get(baseRepo.RepoHost(), "browser")
	return utils.OpenInBrowser(webURL, browser)
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
	opts.io.LogInfof("%s", mr.Title)
	opts.io.LogInfof(c.Gray(" !%d\n"), mr.IID)
	opts.io.LogInfof("%s", mrState(c, mr))
	opts.io.LogInfof(c.Gray(" • @%s opened %s • merge: %s → %s\n"), mr.Author.Username, mrTimeAgo, mr.SourceBranch, mr.TargetBranch)
	opts.io.LogInfo()

	// Description
	if mr.Description != "" {
		mr.Description, _ = utils.RenderMarkdown(mr.Description, opts.io.BackgroundColor())
		opts.io.LogInfo(mr.Description)
	}

	opts.io.LogInfof(c.Gray("\n%d upvotes • %d downvotes • %d comments\n"), mr.Upvotes, mr.Downvotes, mr.UserNotesCount)

	// Meta information
	if labels := labelsList(mr); labels != "" {
		opts.io.LogInfof("%s", c.Bold("Labels: "))
		opts.io.LogInfo(labels)
	}
	if assignees := assigneesList(mr); assignees != "" {
		opts.io.LogInfof("%s", c.Bold("Assignees: "))
		opts.io.LogInfo(assignees)
	}
	if reviewers := reviewersList(mr); reviewers != "" {
		opts.io.LogInfof("%s", c.Bold("Reviewers: "))
		opts.io.LogInfo(reviewers)
	}
	if mr.Milestone != nil {
		opts.io.LogInfof("%s", c.Bold("Milestone: "))
		opts.io.LogInfo(mr.Milestone.Title)
	}
	if mr.State == "closed" {
		if mr.ClosedBy != nil {
			opts.io.LogInfof("Closed by: %s %s\n", mr.ClosedBy.Username, mrTimeAgo)
		} else {
			opts.io.LogInfof("Closed %s\n", mrTimeAgo)
		}
	}
	if mr.Pipeline != nil {
		opts.io.LogInfof("%s", c.Bold("Pipeline status: "))
		var status string
		switch s := mr.Pipeline.Status; s {
		case "failed":
			status = c.Red(s)
		case "success":
			status = c.Green(s)
		default:
			status = c.Gray(s)
		}
		opts.io.LogInfof("%s (View pipeline with `%s`)\n", status, c.Bold("glab ci view "+mr.SourceBranch))

		if mr.MergeWhenPipelineSucceeds && mr.Pipeline.Status != "success" {
			opts.io.LogInfof("%s Requires pipeline to succeed before merging.\n", c.WarnIcon())
		}
	}
	if mrApprovals != nil {
		opts.io.LogInfo(c.Bold("Approvals status:"))
		mrutils.PrintMRApprovalState(opts.io, mrApprovals)
	}
	opts.io.LogInfof("%s This merge request has %s changes.\n", c.GreenCheck(), c.Yellow(mr.ChangesCount))
	if mr.State == "merged" && mr.MergedBy != nil { //nolint:staticcheck
		opts.io.LogInfof("%s The changes were merged into %s by %s %s.\n", c.GreenCheck(), mr.TargetBranch, mr.MergedBy.Name, utils.TimeToPrettyTimeAgo(*mr.MergedAt)) //nolint:staticcheck
	}

	if mr.HasConflicts {
		opts.io.LogInfof(c.Red("%s This branch has conflicts that must be resolved.\n"), c.FailedIcon())
	}

	// Comments
	if opts.showComments {
		opts.io.LogInfo(heredoc.Doc(`
			--------------------------------------------
			Discussions
			--------------------------------------------
			`))
		if len(discussions) > 0 {
			mrutils.PrintDiscussions(out, opts.io, discussions, mrutils.PrintDiscussionsOptions{
				ShowSystemLogs:                 opts.showSystemLogs,
				ShowSingleNoteDiscussionPrefix: false,
			})
		} else {
			// Provide specific message based on filter flags
			if opts.showResolved && !opts.showUnresolved {
				opts.io.LogInfo("This merge request has no resolved threads.")
			} else if opts.showUnresolved && !opts.showResolved {
				opts.io.LogInfo("This merge request has no unresolved threads.")
			} else {
				opts.io.LogInfo("This merge request has no comments.")
			}
		}
	}

	opts.io.LogInfo()
	opts.io.LogInfof(c.Gray("View this merge request on GitLab: %s\n"), mr.WebURL)
}

func printRawMRPreview(opts *options, mr *gitlab.MergeRequest, discussions []*gitlab.Discussion) {
	opts.io.LogInfof("%s", rawMRPreview(opts, mr, discussions))
}

func rawMRPreview(opts *options, mr *gitlab.MergeRequest, discussions []*gitlab.Discussion) string {
	var out strings.Builder

	assignees := assigneesList(mr)
	reviewers := reviewersList(mr)
	labels := labelsList(mr)

	fmt.Fprintf(&out, "title:\t%s\n", mr.Title)                     //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "state:\t%s\n", mrState(opts.io.Color(), mr)) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "author:\t%s\n", mr.Author.Username)          //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "labels:\t%s\n", labels)                      //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "assignees:\t%s\n", assignees)                //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "reviewers:\t%s\n", reviewers)                //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "source_branch:\t%s\n", mr.SourceBranch)      //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "target_branch:\t%s\n", mr.TargetBranch)      //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "comments:\t%d\n", mr.UserNotesCount)         //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	if mr.Milestone != nil {
		fmt.Fprintf(&out, "milestone:\t%s\n", mr.Milestone.Title) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	}
	fmt.Fprintf(&out, "number:\t%d\n", mr.IID) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "url:\t%s\n", mr.WebURL) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "--\n")                  //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	fmt.Fprintf(&out, "%s\n", mr.Description)  //nolint:forbidigo // writing to strings.Builder, not stdout/stderr

	if opts.showComments {
		if len(discussions) > 0 {
			mrutils.PrintDiscussions(&out, opts.io, discussions, mrutils.PrintDiscussionsOptions{
				ShowSystemLogs:                 opts.showSystemLogs,
				ShowSingleNoteDiscussionPrefix: false,
			})
		} else {
			fmt.Fprintln(&out, "This merge request has no comments.") //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
		}
	}

	return out.String()
}

func printJSONMR(opts *options, mr *gitlab.MergeRequest, discussions []*gitlab.Discussion) error {
	if opts.showComments {
		return opts.io.PrintJSON(MRWithDiscussions{mr, discussions})
	}
	return opts.io.PrintJSON(mr)
}
