package view

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var listIssueNotes = func(client *gitlab.Client, projectID any, issueID int64, opts *gitlab.ListIssueNotesOptions) ([]*gitlab.Note, error) {
	if opts.PerPage == 0 {
		opts.PerPage = api.DefaultListLimit
	}
	notes, _, err := client.Notes.ListIssueNotes(projectID, issueID, opts)
	if err != nil {
		return nil, err
	}
	return notes, nil
}

type IssueWithNotes struct {
	*gitlab.Issue
	Notes []*gitlab.Note
}

type options struct {
	showComments   bool
	showSystemLogs bool
	web            bool
	outputFormat   string

	commentPageNumber int
	commentLimit      int

	notes []*gitlab.Note
	issue *gitlab.Issue

	io              *iostreams.IOStreams
	apiClient       func(repoHost string) (*api.Client, error)
	gitlabClient    func() (*gitlab.Client, error)
	config          func() config.Config
	baseRepo        func() (glrepo.Interface, error)
	defaultHostname string
}

func NewCmdView(f cmdutils.Factory, issueType issuable.IssueType) *cobra.Command {
	examplePath := "issues/123"

	if issueType == issuable.TypeIncident {
		examplePath = "issues/incident/123"
	}

	opts := &options{
		io:              f.IO(),
		apiClient:       f.ApiClient,
		gitlabClient:    f.GitLabClient,
		config:          f.Config,
		baseRepo:        f.BaseRepo,
		defaultHostname: f.DefaultHostname(),
	}
	issueViewCmd := &cobra.Command{
		Use:     "view <id>",
		Short:   fmt.Sprintf(`Display the title, body, and other information about an %s.`, issueType),
		Long:    ``,
		Aliases: []string{"show"},
		Example: heredoc.Doc(fmt.Sprintf(`
			$ glab %[1]s view 123
			$ glab %[1]s show 123
			$ glab %[1]s view --web 123
			$ glab %[1]s view --comments 123
			$ glab %[1]s view https://gitlab.com/NAMESPACE/REPO/-/%s
		`, issueType, examplePath)),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(issueType, args)
		},
	}

	issueViewCmd.Flags().BoolVarP(&opts.showComments, "comments", "c", false, fmt.Sprintf("Show %s comments and activities.", issueType))
	issueViewCmd.Flags().BoolVarP(&opts.showSystemLogs, "system-logs", "s", false, "Show system activities and logs.")
	issueViewCmd.Flags().BoolVarP(&opts.web, "web", "w", false, fmt.Sprintf("Open %s in a browser. Uses the default browser, or the browser specified in the $BROWSER variable.", issueType))
	issueViewCmd.Flags().IntVarP(&opts.commentPageNumber, "page", "p", 1, "Page number.")
	issueViewCmd.Flags().IntVarP(&opts.commentLimit, "per-page", "P", 20, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(issueViewCmd, &opts.outputFormat)

	return issueViewCmd
}

func (o *options) run(issueType issuable.IssueType, args []string) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	cfg := o.config()

	issue, baseRepo, err := issueutils.IssueFromArg(o.apiClient, client, o.baseRepo, o.defaultHostname, args[0])
	if err != nil {
		return err
	}

	o.issue = issue

	valid, msg := issuable.ValidateIncidentCmd(issueType, "view", o.issue)
	if !valid {
		fmt.Fprintln(o.io.StdErr, msg)
		return nil
	}

	// open in browser if --web flag is specified
	if o.web {
		if o.io.IsaTTY && o.io.IsErrTTY {
			fmt.Fprintf(o.io.StdErr, "Opening %s in your browser.\n", utils.DisplayURL(o.issue.WebURL))
		}

		browser, _ := cfg.Get(baseRepo.RepoHost(), "browser")
		return utils.OpenInBrowser(o.issue.WebURL, browser)
	}

	if o.showComments {
		l := &gitlab.ListIssueNotesOptions{
			Sort: gitlab.Ptr("asc"),
		}
		if o.commentPageNumber != 0 {
			l.Page = int64(o.commentPageNumber)
		}
		if o.commentLimit != 0 {
			l.PerPage = int64(o.commentLimit)
		}
		o.notes, err = listIssueNotes(client, baseRepo.FullName(), o.issue.IID, l)
		if err != nil {
			return err
		}
	}

	glamourStyle, _ := cfg.Get(baseRepo.RepoHost(), "glamour_style")
	o.io.ResolveBackgroundColor(glamourStyle)
	err = o.io.StartPager()
	if err != nil {
		return err
	}
	defer o.io.StopPager()

	switch {
	case o.outputFormat == "json":
		printJSONIssue(o)
	case o.io.IsErrTTY && o.io.IsaTTY:
		printTTYIssuePreview(o)
	default:
		printRawIssuePreview(o)
	}
	return nil
}

func labelsList(opts *options) string {
	return strings.Join(opts.issue.Labels, ", ")
}

func assigneesList(opts *options) string {
	assignees := utils.Map(opts.issue.Assignees, func(a *gitlab.IssueAssignee) string {
		return a.Username
	})

	return strings.Join(assignees, ", ")
}

func issueState(opts *options, c *iostreams.ColorPalette) string {
	switch opts.issue.State {
	case "opened":
		return c.Green("open")
	case "locked":
		return c.Blue(opts.issue.State)
	default:
		return c.Red(opts.issue.State)
	}
}

func printTTYIssuePreview(opts *options) {
	c := opts.io.Color()
	issueTimeAgo := utils.TimeToPrettyTimeAgo(*opts.issue.CreatedAt)
	// Header
	fmt.Fprint(opts.io.StdOut, issueState(opts, c))
	fmt.Fprintf(opts.io.StdOut, c.Gray(" • opened by %s %s\n"), opts.issue.Author.Username, issueTimeAgo)
	fmt.Fprint(opts.io.StdOut, c.Bold(opts.issue.Title))
	fmt.Fprintf(opts.io.StdOut, c.Gray(" #%d"), opts.issue.IID)
	fmt.Fprintln(opts.io.StdOut)

	// Description
	if opts.issue.Description != "" {
		opts.issue.Description, _ = utils.RenderMarkdown(opts.issue.Description, opts.io.BackgroundColor())
		fmt.Fprintln(opts.io.StdOut, opts.issue.Description)
	}

	fmt.Fprintf(opts.io.StdOut, c.Gray("\n%d upvotes • %d downvotes • %d comments\n"), opts.issue.Upvotes, opts.issue.Downvotes, opts.issue.UserNotesCount)

	// Meta information
	if labels := labelsList(opts); labels != "" {
		fmt.Fprint(opts.io.StdOut, c.Bold("Labels: "))
		fmt.Fprintln(opts.io.StdOut, labels)
	}
	if assignees := assigneesList(opts); assignees != "" {
		fmt.Fprint(opts.io.StdOut, c.Bold("Assignees: "))
		fmt.Fprintln(opts.io.StdOut, assignees)
	}
	if opts.issue.Milestone != nil {
		fmt.Fprint(opts.io.StdOut, c.Bold("Milestone: "))
		fmt.Fprintln(opts.io.StdOut, opts.issue.Milestone.Title)
	}
	if opts.issue.State == "closed" {
		fmt.Fprintf(opts.io.StdOut, "Closed by: %s %s\n", opts.issue.ClosedBy.Username, issueTimeAgo)
	}

	// Comments
	if opts.showComments {
		fmt.Fprintln(opts.io.StdOut, heredoc.Doc(`
			--------------------------------------------
			Comments / Notes
			--------------------------------------------
			`))
		if len(opts.notes) > 0 {
			for _, note := range opts.notes {
				if note.System && !opts.showSystemLogs {
					continue
				}
				createdAt := utils.TimeToPrettyTimeAgo(*note.CreatedAt)
				fmt.Fprint(opts.io.StdOut, note.Author.Username)
				if note.System {
					fmt.Fprintf(opts.io.StdOut, " %s ", note.Body)
					fmt.Fprintln(opts.io.StdOut, c.Gray(createdAt))
				} else {
					body, _ := utils.RenderMarkdown(note.Body, opts.io.BackgroundColor())
					fmt.Fprint(opts.io.StdOut, " commented ")
					fmt.Fprintf(opts.io.StdOut, c.Gray("%s\n"), createdAt)
					fmt.Fprintln(opts.io.StdOut, utils.Indent(body, " "))
				}
				fmt.Fprintln(opts.io.StdOut)
			}
		} else {
			fmt.Fprintf(opts.io.StdOut, "There are no comments on this %s.\n", *opts.issue.IssueType)
		}
	}

	fmt.Fprintf(opts.io.StdOut, c.Gray("\nView this %s on GitLab: %s\n"), *opts.issue.IssueType, opts.issue.WebURL)
}

func printRawIssuePreview(opts *options) {
	fmt.Fprint(opts.io.StdOut, rawIssuePreview(opts))
}

func rawIssuePreview(opts *options) string {
	var out string

	assignees := assigneesList(opts)
	labels := labelsList(opts)

	out += fmt.Sprintf("title:\t%s\n", opts.issue.Title)
	out += fmt.Sprintf("state:\t%s\n", issueState(opts, opts.io.Color()))
	out += fmt.Sprintf("author:\t%s\n", opts.issue.Author.Username)
	out += fmt.Sprintf("labels:\t%s\n", labels)
	out += fmt.Sprintf("comments:\t%d\n", opts.issue.UserNotesCount)
	out += fmt.Sprintf("assignees:\t%s\n", assignees)
	if opts.issue.Milestone != nil {
		out += fmt.Sprintf("milestone:\t%s\n", opts.issue.Milestone.Title)
	}

	out += "--\n"
	out += fmt.Sprintf("%s\n", opts.issue.Description)

	out += RawIssuableNotes(opts.notes, opts.showComments, opts.showSystemLogs, *opts.issue.IssueType)

	return out
}

// RawIssuableNotes returns a list of comments/notes in a raw format
func RawIssuableNotes(notes []*gitlab.Note, showComments bool, showSystemLogs bool, issuableName string) string {
	var out strings.Builder

	if showComments {
		out.WriteString("\n--\ncomments/notes:\n\n")

		if len(notes) > 0 {
			for _, note := range notes {
				if note.System && !showSystemLogs {
					continue
				}

				if note.System {
					out.WriteString(fmt.Sprintf("%s %s %s\n\n", note.Author.Username, note.Body, note.CreatedAt.String()))
				} else {
					out.WriteString(fmt.Sprintf("%s commented %s\n%s\n\n", note.Author.Username, note.CreatedAt.String(), note.Body))
				}
			}
		} else {
			out.WriteString(fmt.Sprintf("There are no comments on this %s.\n", issuableName))
		}
	}

	return out.String()
}

func printJSONIssue(opts *options) {
	// var notes []gitlab.Note
	if opts.showComments {

		extendedIssue := IssueWithNotes{opts.issue, opts.notes}
		issueJSON, _ := json.Marshal(extendedIssue)
		fmt.Fprintln(opts.io.StdOut, string(issueJSON))
	} else {
		issueJSON, _ := json.Marshal(opts.issue)
		fmt.Fprintln(opts.io.StdOut, string(issueJSON))
	}
}
