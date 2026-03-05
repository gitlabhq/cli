package list

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// defaultSortByOrder provides sensible default sort directions for each order option.
// Time-based fields default to desc (newest first), while priority and title default to asc.
var defaultSortByOrder = map[string]string{
	"created_at":     "desc",
	"updated_at":     "desc",
	"merged_at":      "desc",
	"title":          "asc",
	"popularity":     "desc",
	"milestone_due":  "asc",
	"priority":       "asc",
	"label_priority": "asc",
}

type options struct {
	// metadata
	assignee       []string
	author         string
	createdAfter   time.Time
	createdBefore  time.Time
	deployedAfter  time.Time
	deployedBefore time.Time
	environment    string
	group          string
	labels         []string
	milestone      string
	mine           bool
	notLabels      []string
	reviewer       []string
	search         string
	sourceBranch   string
	targetBranch   string

	// issue states
	state    string
	closed   bool
	merged   bool
	all      bool
	draft    bool
	notDraft bool

	// Pagination
	page         int
	perPage      int
	outputFormat string

	// display opts
	listType       string
	titleQualifier string

	// sort options
	sort    string
	orderBy string

	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)
	apiClient func(repoHost string) (*api.Client, error)
}

func NewCmdList(f cmdutils.Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		baseRepo:  f.BaseRepo,
		apiClient: f.ApiClient,
	}

	mrListCmd := &cobra.Command{
		Use:     "list [flags]",
		Short:   `List merge requests.`,
		Long:    ``,
		Aliases: []string{"ls"},
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		Example: heredoc.Doc(`
			$ glab mr list --all
			$ glab mr ls -a
			$ glab mr list --assignee=@me
			$ glab mr list --reviewer=@me
			$ glab mr list --source-branch=new-feature
			$ glab mr list --target-branch=main
			$ glab mr list --search "this adds feature X"
			$ glab mr list --label needs-review
			$ glab mr list --not-label waiting-maintainer-feedback,subsystem-x
			$ glab mr list -M --per-page 10
			$ glab mr list --draft
			$ glab mr list --not-draft
		`),
		Args: cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}

			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(mrListCmd, f)
	mrListCmd.Flags().StringSliceVarP(&opts.labels, "label", "l", []string{}, "Filter merge request by label <name>. Multiple labels can be comma-separated or specified by repeating the flag.")
	mrListCmd.Flags().StringSliceVar(&opts.notLabels, "not-label", []string{}, "Filter merge requests by not having label <name>. Multiple labels can be comma-separated or specified by repeating the flag.")
	mrListCmd.Flags().StringVar(&opts.author, "author", "", "Filter merge request by author <username>.")
	mrListCmd.Flags().StringVarP(&opts.milestone, "milestone", "m", "", "Filter merge request by milestone <id>.")
	mrListCmd.Flags().StringVarP(&opts.sourceBranch, "source-branch", "s", "", "Filter by source branch <name>.")
	mrListCmd.Flags().StringVarP(&opts.targetBranch, "target-branch", "t", "", "Filter by target branch <name>.")
	mrListCmd.Flags().StringVar(&opts.search, "search", "", "Filter by <string> in title and description.")
	mrListCmd.Flags().BoolVarP(&opts.all, "all", "A", false, "Get all merge requests.")
	mrListCmd.Flags().BoolVarP(&opts.closed, "closed", "c", false, "Get only closed merge requests.")
	mrListCmd.Flags().BoolVarP(&opts.merged, "merged", "M", false, "Get only merged merge requests.")
	mrListCmd.Flags().BoolVarP(&opts.draft, "draft", "d", false, "Filter by draft merge requests.")
	mrListCmd.Flags().BoolVarP(&opts.notDraft, "not-draft", "", false, "Filter by non-draft merge requests.")
	cmdutils.EnableJSONOutput(mrListCmd, &opts.outputFormat)
	mrListCmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	mrListCmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	mrListCmd.Flags().StringSliceVarP(&opts.assignee, "assignee", "a", []string{}, "Get only merge requests assigned to users. Multiple users can be comma-separated or specified by repeating the flag.")
	mrListCmd.Flags().StringSliceVarP(&opts.reviewer, "reviewer", "r", []string{}, "Get only merge requests with users as reviewer. Multiple users can be comma-separated or specified by repeating the flag.")
	mrListCmd.Flags().StringVarP(&opts.sort, "sort", "S", "", "Sort direction for --order field: asc or desc.")
	mrListCmd.Flags().StringVarP(&opts.orderBy, "order", "o", "", "Order merge requests by <field>. Order options: created_at, updated_at, merged_at, title, priority, label_priority, milestone_due, and popularity.")
	mrListCmd.Flags().TimeVar(&opts.createdBefore, "created-before", time.Time{}, []string{time.RFC3339}, "Filter merge requests created before a certain date (ISO 8601 format).")
	mrListCmd.Flags().TimeVar(&opts.createdAfter, "created-after", time.Time{}, []string{time.RFC3339}, "Filter merge requests created after a certain date (ISO 8601 format).")
	mrListCmd.Flags().TimeVar(&opts.deployedBefore, "deployed-before", time.Time{}, []string{time.RFC3339}, "Filter merge requests deployed before a certain date (ISO 8601 format).")
	mrListCmd.Flags().TimeVar(&opts.deployedAfter, "deployed-after", time.Time{}, []string{time.RFC3339}, "Filter merge requests deployed after a certain date (ISO 8601 format).")
	mrListCmd.Flags().StringVar(&opts.environment, "environment", "", "Filter merge requests deployed to the given environment <name>.")

	mrListCmd.Flags().BoolP("opened", "O", false, "Get only open merge requests.")
	_ = mrListCmd.Flags().MarkHidden("opened")
	_ = mrListCmd.Flags().MarkDeprecated("opened", "default value if neither --closed, --locked or --merged is used.")

	mrListCmd.Flags().BoolVarP(&opts.mine, "mine", "", false, "Get only merge requests assigned to me.")
	_ = mrListCmd.Flags().MarkHidden("mine")
	_ = mrListCmd.Flags().MarkDeprecated("mine", "use --assignee=@me.")
	mrListCmd.PersistentFlags().StringP("group", "g", "", "Select a group/subgroup. This option is ignored if a repo argument is set.")
	mrListCmd.MarkFlagsMutuallyExclusive("draft", "not-draft")
	mrListCmd.MarkFlagsMutuallyExclusive("label", "not-label")
	mrListCmd.MarkFlagsMutuallyExclusive("closed", "merged")

	return mrListCmd
}

func (o *options) complete(cmd *cobra.Command) error {
	if o.all {
		o.state = "all"
	} else if o.closed {
		o.state = "closed"
		o.titleQualifier = o.state
	} else if o.merged {
		o.state = "merged"
		o.titleQualifier = o.state
	} else {
		o.state = "opened"
		o.titleQualifier = "open"
	}

	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	// Apply sensible default sort direction if user didn't explicitly set --sort
	sortFlagChanged := cmd.Flags().Changed("sort")
	if !sortFlagChanged && o.orderBy != "" {
		if defaultSort, ok := defaultSortByOrder[o.orderBy]; ok {
			o.sort = defaultSort
		}
	}

	return nil
}

func (o *options) run() error {
	var mergeRequests []*gitlab.BasicMergeRequest

	// NOTE: this command can not only be used for projects,
	// so we have to manually check for the base repo, if it doesn't exist,
	// we bootstrap the client with the default hostname.
	var repoHost string
	if baseRepo, err := o.baseRepo(); err == nil {
		repoHost = baseRepo.RepoHost()
	}
	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	l := &gitlab.ListProjectMergeRequestsOptions{
		State: gitlab.Ptr(o.state),
		ListOptions: gitlab.ListOptions{
			Page:    1,
			PerPage: 30,
		},
	}
	jsonOutput := o.outputFormat == "json"
	if jsonOutput {
		l.Page = 0
		l.PerPage = 0
	}

	if o.author != "" {
		u, err := api.UserByName(client, o.author)
		if err != nil {
			return err
		}
		l.AuthorID = gitlab.Ptr(u.ID)
		o.listType = "search"
	}
	if o.sourceBranch != "" {
		l.SourceBranch = gitlab.Ptr(o.sourceBranch)
		o.listType = "search"
	}
	if o.targetBranch != "" {
		l.TargetBranch = gitlab.Ptr(o.targetBranch)
		o.listType = "search"
	}
	if o.search != "" {
		l.Search = gitlab.Ptr(o.search)
		o.listType = "search"
	}
	if len(o.labels) > 0 {
		l.Labels = (*gitlab.LabelOptions)(&o.labels)
		o.listType = "search"
	}
	if len(o.notLabels) > 0 {
		l.NotLabels = (*gitlab.LabelOptions)(&o.notLabels)
		o.listType = "search"
	}
	if o.milestone != "" {
		l.Milestone = gitlab.Ptr(o.milestone)
		o.listType = "search"
	}
	if o.page != 0 {
		l.Page = int64(o.page)
	}
	if o.perPage != 0 {
		l.PerPage = int64(o.perPage)
	}
	if o.draft {
		l.WIP = gitlab.Ptr("yes")
		o.listType = "search"
	}
	if o.notDraft {
		l.WIP = gitlab.Ptr("no")
		o.listType = "search"
	}

	if o.mine {
		l.Scope = gitlab.Ptr("assigned_to_me")
		o.listType = "search"
	}

	if o.orderBy != "" {
		l.OrderBy = gitlab.Ptr(o.orderBy)
		o.listType = "search"
	}

	if o.sort != "" {
		l.Sort = gitlab.Ptr(o.sort)
	}

	assigneeIds := make([]int, 0)
	if len(o.assignee) > 0 {
		if o.assignee[0] == "@any" {
			l.AssigneeID = gitlab.AssigneeID(gitlab.UserIDAny)
		} else {
			users, err := api.UsersByNames(client, o.assignee)
			if err != nil {
				return err
			}
			for _, user := range users {
				assigneeIds = append(assigneeIds, int(user.ID))
			}
		}
	}

	reviewerIds := make([]int, 0)
	if len(o.reviewer) > 0 {
		if o.reviewer[0] == "@any" {
			l.ReviewerID = gitlab.ReviewerID(gitlab.UserIDAny)
		} else {
			users, err := api.UsersByNames(client, o.reviewer)
			if err != nil {
				return err
			}
			for _, user := range users {
				reviewerIds = append(reviewerIds, int(user.ID))
			}
		}
	}
	title := utils.NewListTitle(o.titleQualifier + " merge request")

	if !o.createdBefore.IsZero() {
		l.CreatedBefore = gitlab.Ptr(o.createdBefore)
	}

	if !o.createdAfter.IsZero() {
		l.CreatedAfter = gitlab.Ptr(o.createdAfter)
	}

	if !o.deployedBefore.IsZero() {
		l.DeployedBefore = gitlab.Ptr(o.deployedBefore)
	}

	if !o.deployedAfter.IsZero() {
		l.DeployedAfter = gitlab.Ptr(o.deployedAfter)
	}

	if o.environment != "" {
		l.Environment = gitlab.Ptr(o.environment)
	}

	if o.group != "" {
		mergeRequests, err = api.ListGroupMRs(client, o.group, projectListMROptionsToGroup(l), api.WithMRAssignees(assigneeIds), api.WithMRReviewers(reviewerIds))
		title.RepoName = o.group
	} else {
		var repo glrepo.Interface
		repo, err = o.baseRepo()
		if err != nil {
			return err
		}

		title.RepoName = repo.FullName()
		mergeRequests, err = api.ListMRs(client, repo.FullName(), l, api.WithMRAssignees(assigneeIds), api.WithMRReviewers(reviewerIds))
	}
	if err != nil {
		return err
	}

	title.Page = int(l.Page)
	title.ListActionType = o.listType
	title.CurrentPageTotal = len(mergeRequests)

	if jsonOutput {
		mrListJSON, _ := json.Marshal(mergeRequests)
		fmt.Fprintln(o.io.StdOut, string(mrListJSON))
	} else {
		if err = o.io.StartPager(); err != nil {
			return err
		}
		defer o.io.StopPager()
		fmt.Fprintf(o.io.StdOut, "%s\n%s\n", title.Describe(), mrutils.DisplayAllMRs(o.io, mergeRequests))
	}
	return nil
}

func projectListMROptionsToGroup(l *gitlab.ListProjectMergeRequestsOptions) *gitlab.ListGroupMergeRequestsOptions {
	return &gitlab.ListGroupMergeRequestsOptions{
		ListOptions:            l.ListOptions,
		State:                  l.State,
		OrderBy:                l.OrderBy,
		Sort:                   l.Sort,
		Milestone:              l.Milestone,
		View:                   l.View,
		Labels:                 l.Labels,
		NotLabels:              l.NotLabels,
		WithLabelsDetails:      l.WithLabelsDetails,
		WithMergeStatusRecheck: l.WithMergeStatusRecheck,
		CreatedAfter:           l.CreatedAfter,
		CreatedBefore:          l.CreatedBefore,
		UpdatedAfter:           l.UpdatedAfter,
		UpdatedBefore:          l.UpdatedBefore,
		Scope:                  l.Scope,
		AuthorID:               l.AuthorID,
		AssigneeID:             l.AssigneeID,
		ReviewerID:             l.ReviewerID,
		ReviewerUsername:       l.ReviewerUsername,
		MyReactionEmoji:        l.MyReactionEmoji,
		SourceBranch:           l.SourceBranch,
		TargetBranch:           l.TargetBranch,
		Search:                 l.Search,
		WIP:                    l.WIP,
	}
}
