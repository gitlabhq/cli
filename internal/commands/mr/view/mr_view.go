package view

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	issuableView "gitlab.com/gitlab-org/cli/internal/commands/issuable/view"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var listMRDiscussions = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.ListMergeRequestDiscussionsOptions) ([]*gitlab.Discussion, error) {
	if opts.PerPage == 0 {
		opts.PerPage = api.DefaultListLimit
	}

	var allDiscussions []*gitlab.Discussion
	page := opts.Page
	if page == 0 {
		page = 1
	}

	for {
		opts.Page = page
		discussions, resp, err := client.Discussions.ListMergeRequestDiscussions(projectID, mrID, opts)
		if err != nil {
			return nil, err
		}

		allDiscussions = append(allDiscussions, discussions...)

		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	return allDiscussions, nil
}

// filterDiscussionsByResolution filters discussions based on their resolution status.
// A discussion is considered resolved if all its resolvable notes are resolved.
// A discussion is considered unresolved if it has at least one resolvable note that is not resolved.
func filterDiscussionsByResolution(discussions []*gitlab.Discussion, showResolved, showUnresolved bool) []*gitlab.Discussion {
	filtered := []*gitlab.Discussion{}

	for _, discussion := range discussions {
		// Check if discussion has any resolvable notes
		hasResolvableNotes := false
		allResolved := true

		for _, note := range discussion.Notes {
			if note.Resolvable {
				hasResolvableNotes = true
				if !note.Resolved {
					allResolved = false
				}
			}
		}

		// Skip discussions without resolvable notes
		if !hasResolvableNotes {
			continue
		}

		// Include based on filter
		if (showResolved && allResolved) || (showUnresolved && !allResolved) {
			filtered = append(filtered, discussion)
		}
	}

	return filtered
}

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
		Use:     "view {<id> | <branch>}",
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
		IncludeDivergedCommitsCount: gitlab.Ptr(true),
		RenderHTML:                  gitlab.Ptr(true),
		IncludeRebaseInProgress:     gitlab.Ptr(true),
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

		discussions, err = listMRDiscussions(client, baseRepo.FullName(), mr.IID, l)
		if err != nil {
			return err
		}

		// Filter discussions based on resolution status
		if o.showResolved || o.showUnresolved {
			discussions = filterDiscussionsByResolution(discussions, o.showResolved, o.showUnresolved)
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
			for _, discussion := range discussions {
				if len(discussion.Notes) == 0 {
					continue
				}

				// Skip system notes unless --system-logs is specified
				firstNote := discussion.Notes[0]
				if firstNote.System && !opts.showSystemLogs {
					continue
				}

				// For threaded discussions (not individual notes)
				if !discussion.IndividualNote && len(discussion.Notes) > 1 {
					// Print thread header with first note ID
					fmt.Fprintf(out, "Thread [#%d]", firstNote.ID)

					// Show resolution status if resolvable
					if firstNote.Resolvable {
						if firstNote.Resolved {
							fmt.Fprint(out, c.Green(" ✓ resolved"))
						} else {
							fmt.Fprint(out, c.Yellow(" ⚠ unresolved"))
						}
					}
					fmt.Fprintln(out)

					// Print first note
					createdAt := utils.TimeToPrettyTimeAgo(*firstNote.CreatedAt)
					fmt.Fprintf(out, "  @%s commented ", firstNote.Author.Username)
					fmt.Fprintln(out, c.Gray(createdAt))

					if firstNote.Position != nil {
						printCommentFileContext(out, c, firstNote.Position)
					}

					body, _ := utils.RenderMarkdown(firstNote.Body, opts.io.BackgroundColor())
					fmt.Fprintln(out, utils.Indent(body, "  "))
					fmt.Fprintln(out)

					// Print replies (indented)
					for i, note := range discussion.Notes[1:] {
						if note.System && !opts.showSystemLogs {
							continue
						}
						replyTime := utils.TimeToPrettyTimeAgo(*note.CreatedAt)
						fmt.Fprintf(out, "    @%s replied ", note.Author.Username)
						fmt.Fprintln(out, c.Gray(replyTime))

						replyBody, _ := utils.RenderMarkdown(note.Body, opts.io.BackgroundColor())
						fmt.Fprintln(out, utils.Indent(replyBody, "    "))
						if i < len(discussion.Notes[1:])-1 {
							fmt.Fprintln(out)
						}
					}
					fmt.Fprintln(out)
				} else {
					// Individual note (not a thread)
					note := firstNote
					createdAt := utils.TimeToPrettyTimeAgo(*note.CreatedAt)
					fmt.Fprint(out, "@", note.Author.Username)
					if note.System {
						fmt.Fprintf(out, " %s ", note.Body)
						fmt.Fprintln(out, c.Gray(createdAt))
					} else {
						body, _ := utils.RenderMarkdown(note.Body, opts.io.BackgroundColor())
						fmt.Fprint(out, " commented ")
						fmt.Fprintf(out, c.Gray("%s\n"), createdAt)

						// Display file and line context if available
						if note.Position != nil {
							printCommentFileContext(out, c, note.Position)
						}

						fmt.Fprintln(out, utils.Indent(body, " "))
					}
					fmt.Fprintln(out)
				}
			}
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
	var out string

	assignees := assigneesList(mr)
	reviewers := reviewersList(mr)
	labels := labelsList(mr)

	out += fmt.Sprintf("title:\t%s\n", mr.Title)
	out += fmt.Sprintf("state:\t%s\n", mrState(opts.io.Color(), mr))
	out += fmt.Sprintf("author:\t%s\n", mr.Author.Username)
	out += fmt.Sprintf("labels:\t%s\n", labels)
	out += fmt.Sprintf("assignees:\t%s\n", assignees)
	out += fmt.Sprintf("reviewers:\t%s\n", reviewers)
	out += fmt.Sprintf("comments:\t%d\n", mr.UserNotesCount)
	if mr.Milestone != nil {
		out += fmt.Sprintf("milestone:\t%s\n", mr.Milestone.Title)
	}
	out += fmt.Sprintf("number:\t%d\n", mr.IID)
	out += fmt.Sprintf("url:\t%s\n", mr.WebURL)
	out += "--\n"
	out += fmt.Sprintf("%s\n", mr.Description)

	// Flatten discussions to notes for raw output
	var notes []*gitlab.Note
	if opts.showComments {
		for _, discussion := range discussions {
			for _, note := range discussion.Notes {
				if note.System && !opts.showSystemLogs {
					continue
				}
				notes = append(notes, note)
			}
		}
		// Sort notes chronologically by creation time
		sort.Slice(notes, func(i, j int) bool {
			return notes[i].CreatedAt.Before(*notes[j].CreatedAt)
		})
	}
	out += issuableView.RawIssuableNotes(notes, opts.showComments, opts.showSystemLogs, "merge request")

	return out
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

func printCommentFileContext(out io.Writer, c *iostreams.ColorPalette, pos *gitlab.NotePosition) {
	// Check for multi-line comment first
	if pos.LineRange != nil && pos.LineRange.StartRange != nil && pos.LineRange.EndRange != nil {
		startLine := pos.LineRange.StartRange.NewLine
		endLine := pos.LineRange.EndRange.NewLine

		// Fall back to old line numbers if new ones aren't available
		if startLine == 0 {
			startLine = pos.LineRange.StartRange.OldLine
		}
		if endLine == 0 {
			endLine = pos.LineRange.EndRange.OldLine
		}

		// Display range if we have valid start and end lines
		if startLine > 0 && endLine > 0 {
			filePath := pos.NewPath
			if filePath == "" {
				filePath = pos.OldPath
			}
			if filePath != "" {
				if startLine != endLine {
					fmt.Fprintf(out, " on %s:%d-%d\n", c.Cyan(filePath), startLine, endLine)
				} else {
					fmt.Fprintf(out, " on %s:%d\n", c.Cyan(filePath), startLine)
				}
				return
			}
		}
	}

	// Fall back to single-line comment
	if pos.NewPath != "" && pos.NewLine > 0 {
		fmt.Fprintf(out, " on %s:%d\n", c.Cyan(pos.NewPath), pos.NewLine)
	} else if pos.OldPath != "" && pos.OldLine > 0 {
		fmt.Fprintf(out, " on %s:%d\n", c.Cyan(pos.OldPath), pos.OldLine)
	}
}
