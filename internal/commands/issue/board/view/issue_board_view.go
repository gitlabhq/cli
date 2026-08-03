package view

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	closed string = "closed"
	opened string = "opened"

	// maxConcurrentListFetches caps the number of board lists whose issues
	// are fetched in parallel, to avoid overwhelming the API.
	maxConcurrentListFetches = 4
)

type issueBoardViewOptions struct {
	assignee  string
	labels    []string
	milestone string
	state     string
	paginate  bool
}

type boardMeta struct {
	name    string
	id      int64
	group   *gitlab.Group
	project *gitlab.Project
}

// listResult holds the issues fetched for a single board list, along with
// the state used to fetch them (needed by filterIssues to render the list).
type listResult struct {
	issues []*gitlab.Issue
	state  string
}

func NewCmdView(f cmdutils.Factory) *cobra.Command {
	opts := &issueBoardViewOptions{}
	viewCmd := &cobra.Command{
		Use:   "view [flags]",
		Short: `View project issue board.`,
		Long: heredoc.Doc(`
			Opens an interactive view of the project's issue boards in your
			terminal, where you can browse issues by list.
		`),
		Example: heredoc.Doc(`
			glab issue board view`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a := tview.NewApplication()
			defer recoverPanic(a)

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			project, err := api.GetProject(client, repo.FullName())
			if err != nil {
				return fmt.Errorf("failed to get project: %w", err)
			}

			// List the groups that are ancestors to project:
			// https://docs.gitlab.com/api/projects/#list-groups
			projectGroups, _, err := client.Projects.ListProjectsGroups(project.ID, &gitlab.ListProjectGroupOptions{})
			if err != nil {
				return err
			}

			menuOptions, boardMetaMap, err := fetchIssueBoards(cmd.Context(), client, repo, projectGroups)
			if err != nil {
				return err
			}

			selection, err := selectBoard(cmd.Context(), f.IO(), menuOptions)
			if err != nil {
				return fmt.Errorf("selecting issue board: %w", err)
			}
			selectedBoard := boardMetaMap[selection]

			boardLists, err := getBoardLists(client, selectedBoard, repo)
			if err != nil {
				return fmt.Errorf("getting issue board lists: %w", err)
			}

			results, err := fetchAllListIssues(cmd.Context(), client, selectedBoard, repo, boardLists, opts)
			if err != nil {
				return err
			}

			root := buildBoardFlex(boardLists, results)
			root.SetBorderPadding(1, 1, 2, 2).SetBorder(true).SetTitle(boardTitle(selectedBoard, project))

			screen, err := tcell.NewScreen()
			if err != nil {
				return err
			}
			if err := a.SetScreen(screen).SetRoot(root, true).Run(); err != nil {
				return err
			}
			return nil
		},
	}

	viewCmd.Flags().
		StringVarP(&opts.assignee, "assignee", "a", "", "Filter board issues by assignee username.")
	viewCmd.Flags().
		StringSliceVarP(&opts.labels, "labels", "l", []string{}, "Filter board issues by labels. Multiple labels can be comma-separated or specified by repeating the flag.")
	viewCmd.Flags().
		StringVarP(&opts.milestone, "milestone", "m", "", "Filter board issues by milestone.")
	viewCmd.Flags().
		BoolVar(&opts.paginate, "paginate", false, "Make additional HTTP requests to retrieve all board issues.")
	return viewCmd
}

func (opts *issueBoardViewOptions) getListProjectIssueOptions() *gitlab.ListProjectIssuesOptions {
	withLabelDetails := true
	reqOpts := &gitlab.ListProjectIssuesOptions{
		WithLabelDetails: &withLabelDetails,
	}

	if opts.assignee != "" {
		reqOpts.AssigneeUsername = &opts.assignee
	}

	if len(opts.labels) != 0 {
		labels := gitlab.LabelOptions(opts.labels)
		reqOpts.Labels = &labels
	}

	if opts.state != "" {
		reqOpts.State = &opts.state
	}

	if opts.milestone != "" {
		reqOpts.Milestone = &opts.milestone
	}
	return reqOpts
}

func (opts *issueBoardViewOptions) getListGroupIssueOptions() *gitlab.ListGroupIssuesOptions {
	withLabelDetails := true
	reqOpts := &gitlab.ListGroupIssuesOptions{
		WithLabelDetails: &withLabelDetails,
	}

	if opts.assignee != "" {
		reqOpts.AssigneeUsername = &opts.assignee
	}

	if len(opts.labels) != 0 {
		labels := gitlab.LabelOptions(opts.labels)
		reqOpts.Labels = &labels
	}

	if opts.state != "" {
		reqOpts.State = &opts.state
	}

	if opts.milestone != "" {
		reqOpts.Milestone = &opts.milestone
	}
	return reqOpts
}

func recoverPanic(app *tview.Application) {
	if r := recover(); r != nil {
		app.Stop()
		log.Fatalf("%s\n%s\n", r, string(debug.Stack()))
	}
}

func buildLabelString(labelDetails []*gitlab.LabelDetails) string {
	var labels string
	for _, ld := range labelDetails {
		labels += fmt.Sprintf("[white:%s:-]%s[white:-:-] ", ld.Color, ld.Name)
	}
	if labels != "" {
		labels = strings.TrimSpace(labels) + "\n"
	}
	return labels
}

func selectBoard(ctx context.Context, io *iostreams.IOStreams, menuOptions []string) (string, error) {
	options := make([]huh.Option[string], 0, len(menuOptions))
	for _, opt := range menuOptions {
		options = append(options, huh.NewOption(opt, opt))
	}

	var selectedOption string
	selector := huh.NewSelect[string]().
		Title("Select board:").
		Options(options...).
		Value(&selectedOption)

	err := io.Run(ctx, selector)
	if err != nil {
		return "", err
	}
	return selectedOption, nil
}

// mapBoardData takes project and group issue board slices and
// returns menu options for the user selection and a map of the board metadata keyed by the menu options
func mapBoardData(
	projectIssueBoards []*gitlab.IssueBoard,
	projectGroupIssueBoards []*gitlab.GroupIssueBoard,
) ([]string, map[string]boardMeta) {
	// find longest board name to base padding on
	maxNameLength := 0
	for _, board := range projectIssueBoards {
		if len(board.Name) > maxNameLength {
			maxNameLength = len(board.Name)
		}
	}
	for _, board := range projectGroupIssueBoards {
		if len(board.Name) > maxNameLength {
			maxNameLength = len(board.Name)
		}
	}

	minPadding := 3
	menuOptions := []string{}
	boardMetaMap := map[string]boardMeta{}

	formatMenuOption := func(boardName, parentName string, padding int, isGroupBoard bool) string {
		sb := strings.Builder{}
		sb.WriteString(boardName)
		sb.WriteString(strings.Repeat(" ", padding))
		kind := "PROJECT"
		if isGroupBoard {
			kind = "GROUP"
		}
		sb.WriteString(fmt.Sprintf("(%s: %s)", kind, parentName))
		return sb.String()
	}

	// build menu entries and map metadata
	for _, board := range projectGroupIssueBoards {
		padding := max(maxNameLength-len(board.Name)+3, minPadding)

		option := formatMenuOption(board.Name, board.Group.Name, padding, true)
		menuOptions = append(menuOptions, option)
		boardMetaMap[option] = boardMeta{
			id:    board.ID,
			name:  board.Name,
			group: board.Group,
		}
	}

	for _, board := range projectIssueBoards {
		padding := max(maxNameLength-len(board.Name)+3, minPadding)

		option := formatMenuOption(board.Name, board.Project.Name, padding, false)
		menuOptions = append(menuOptions, option)
		boardMetaMap[option] = boardMeta{
			id:      board.ID,
			name:    board.Name,
			project: board.Project,
		}
	}
	return menuOptions, boardMetaMap
}

// fetchIssueBoards retrieves project and group issue boards in parallel and
// maps them into menu options for user selection.
// https://docs.gitlab.com/api/group_boards/#list-group-issue-board-lists
func fetchIssueBoards(
	ctx context.Context,
	client *gitlab.Client,
	repo glrepo.Interface,
	projectGroups []*gitlab.ProjectGroup,
) ([]string, map[string]boardMeta, error) {
	var projectIssueBoards []*gitlab.IssueBoard
	var projectGroupIssueBoards []*gitlab.GroupIssueBoard

	g, _ := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		projectIssueBoards, err = getProjectIssueBoards(client, repo)
		if err != nil {
			return fmt.Errorf("getting project issue boards: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		projectGroupIssueBoards, err = getGroupIssueBoards(projectGroups, client)
		if err != nil {
			return fmt.Errorf("getting group issue boards: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	menuOptions, boardMetaMap := mapBoardData(projectIssueBoards, projectGroupIssueBoards)
	return menuOptions, boardMetaMap, nil
}

// fetchAllListIssues fetches the issues for every board list in parallel,
// keeping results in the original board list order.
func fetchAllListIssues(
	ctx context.Context,
	client *gitlab.Client,
	board boardMeta,
	repo glrepo.Interface,
	boardLists []*gitlab.BoardList,
	opts *issueBoardViewOptions,
) ([]listResult, error) {
	results := make([]listResult, len(boardLists))

	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentListFetches)

	for i, l := range boardLists {
		if l.Label == nil {
			continue
		}
		listOpts := *opts
		switch l.Label.Name {
		case "Closed":
			listOpts.state = closed
		case "Open":
			listOpts.state = opened
		}
		g.Go(func() error {
			var issues []*gitlab.Issue
			var err error
			if board.group != nil {
				issues, err = getGroupBoardIssues(client, board.group.ID, &listOpts)
			} else {
				issues, err = getProjectBoardIssues(client, repo, &listOpts)
			}
			if err != nil {
				return fmt.Errorf("getting issues for list %s: %w", l.Label.Name, err)
			}
			results[i] = listResult{issues: issues, state: listOpts.state}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// buildBoardFlex renders the pre-fetched issues for each board list into a
// horizontal tview.Flex, one bordered text view per list.
func buildBoardFlex(boardLists []*gitlab.BoardList, results []listResult) *tview.Flex {
	root := tview.NewFlex()
	root.SetBackgroundColor(tcell.ColorDefault)
	for i, l := range boardLists {
		if l.Label == nil {
			continue
		}
		boardIssues := filterIssues(boardLists, results[i].issues, l, results[i].state)
		bx := tview.NewTextView()
		bx.
			SetDynamicColors(true).
			SetText(boardIssues).
			SetWrap(true).
			SetBackgroundColor(tcell.ColorDefault).
			SetBorder(true).
			SetTitle(l.Label.Name).
			SetTitleColor(tcell.GetColor(l.Label.Color))
		root.AddItem(bx, 0, 1, false)
	}
	return root
}

// boardTitle formats the window title for the selected board, distinguishing
// group boards from project boards.
func boardTitle(board boardMeta, project *gitlab.Project) string {
	boardType := "group"
	boardContext := project.Namespace.Name
	if board.group == nil {
		boardType = "project"
		boardContext = project.NameWithNamespace
	}
	caser := cases.Title(language.English)
	return fmt.Sprintf(" %s • %s ", caser.String(boardType+" issue board"), boardContext)
}

func getProjectIssueBoards(apiClient *gitlab.Client, repo glrepo.Interface) ([]*gitlab.IssueBoard, error) {
	projectIssueBoards, _, err := apiClient.Boards.ListIssueBoards(repo.FullName(), &gitlab.ListIssueBoardsOptions{})
	if err != nil {
		return nil, fmt.Errorf("retrieving issue board: %w", err)
	}
	return projectIssueBoards, nil
}

func getGroupIssueBoards(
	projectGroups []*gitlab.ProjectGroup,
	gitlabClient *gitlab.Client,
) ([]*gitlab.GroupIssueBoard, error) {
	if len(projectGroups) == 0 {
		return []*gitlab.GroupIssueBoard{}, nil
	}

	results := make([][]*gitlab.GroupIssueBoard, len(projectGroups))
	g, _ := errgroup.WithContext(context.Background())

	for i, pg := range projectGroups {
		g.Go(func() error {
			boards, _, err := gitlabClient.GroupIssueBoards.ListGroupIssueBoards(
				pg.ID,
				&gitlab.ListGroupIssueBoardsOptions{},
			)
			if err != nil {
				return fmt.Errorf("retrieving issue board: %w", err)
			}
			results[i] = boards
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var out []*gitlab.GroupIssueBoard
	for _, boards := range results {
		out = append(out, boards...)
	}
	return out, nil
}

// getBoardLists fetches a board's lists from the group or project API and
// pads them with synthetic 'Open'/'Closed' lists used to render issues that
// don't carry a board-list label.
func getBoardLists(apiClient *gitlab.Client, board boardMeta, repo glrepo.Interface) ([]*gitlab.BoardList, error) {
	boardLists, err := fetchBoardLists(apiClient, board, repo)
	if err != nil {
		return nil, err
	}
	return withOpenClosedLists(boardLists), nil
}

func fetchBoardLists(apiClient *gitlab.Client, board boardMeta, repo glrepo.Interface) ([]*gitlab.BoardList, error) {
	if board.group != nil {
		boardLists, _, err := apiClient.GroupIssueBoards.ListGroupIssueBoardLists(board.group.ID, board.id, &gitlab.ListGroupIssueBoardListsOptions{})
		if err != nil {
			return nil, err
		}
		return boardLists, nil
	}

	boardLists, _, err := apiClient.Boards.GetIssueBoardLists(repo.FullName(), board.id, &gitlab.GetIssueBoardListsOptions{})
	if err != nil {
		return nil, err
	}
	return boardLists, nil
}

// withOpenClosedLists prepends/appends the empty 'opened' and 'closed' lists
// used later when reading issues into the table view.
func withOpenClosedLists(boardLists []*gitlab.BoardList) []*gitlab.BoardList {
	opened := &gitlab.BoardList{
		Label: &gitlab.Label{
			Name:      "Open",
			Color:     "#fabd2f",
			TextColor: "#000000",
		},
		Position: 0,
	}
	boardLists = append([]*gitlab.BoardList{opened}, boardLists...)

	closed := &gitlab.BoardList{
		Label: &gitlab.Label{
			Name:      "Closed",
			Color:     "#8ec07c",
			TextColor: "#000000",
		},
		Position: int64(len(boardLists)),
	}
	return append(boardLists, closed)
}

func getGroupBoardIssues(apiClient *gitlab.Client, groupID int64, opts *issueBoardViewOptions) ([]*gitlab.Issue, error) {
	reqOpts := opts.getListGroupIssueOptions()
	if reqOpts.PerPage == 0 {
		reqOpts.PerPage = api.DefaultListLimit
	}
	var issues []*gitlab.Issue
	var err error
	if opts.paginate {
		issues, err = gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
			return apiClient.Issues.ListGroupIssues(groupID, reqOpts, p)
		})
	} else {
		issues, _, err = apiClient.Issues.ListGroupIssues(groupID, reqOpts)
	}
	if err != nil {
		return nil, fmt.Errorf("retrieving list issues: %w", err)
	}
	return issues, nil
}

func getProjectBoardIssues(apiClient *gitlab.Client, repo glrepo.Interface, opts *issueBoardViewOptions) ([]*gitlab.Issue, error) {
	reqOpts := opts.getListProjectIssueOptions()
	if reqOpts.PerPage == 0 {
		reqOpts.PerPage = api.DefaultListLimit
	}
	var issues []*gitlab.Issue
	var err error
	if opts.paginate {
		issues, err = gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
			return apiClient.Issues.ListProjectIssues(repo.FullName(), reqOpts, p)
		})
	} else {
		issues, _, err = apiClient.Issues.ListProjectIssues(repo.FullName(), reqOpts)
	}
	if err != nil {
		return nil, fmt.Errorf("retrieving list issues: %w", err)
	}
	return issues, nil
}

// issueBelongsToOtherList reports whether issue carries a label matching any
// board list, meaning it belongs on that list rather than the open list.
func issueBelongsToOtherList(issue *gitlab.Issue, boardLists []*gitlab.BoardList) bool {
	return slices.ContainsFunc(boardLists, func(boardList *gitlab.BoardList) bool {
		return slices.Contains(issue.Labels, boardList.Label.Name)
	})
}

// filterIssues scans through the issues passed to it, filtering for the ones that belong in targetList
// This function returns a string representation of the issues for targetList which will be displayed in the table view
func filterIssues(
	boardLists []*gitlab.BoardList,
	issues []*gitlab.Issue,
	targetList *gitlab.BoardList,
	state string,
) string {
	var boardIssues strings.Builder
next:
	for _, issue := range issues {
		switch state {
		// skip all issues that are not in the "closed" state for the "closed" list
		case closed:
			if issue.State != closed {
				continue next
			}
		// skip issues labeled for other board lists when populating the "open" list
		case opened:
			if issueBelongsToOtherList(issue, boardLists) {
				continue next
			}
		// filter labeled issues into board lists with corresponding labels
		default:
			var hasListLabel bool
			if slices.Contains(issue.Labels, targetList.Label.Name) {
				hasListLabel = true
			}
			if !hasListLabel || issue.State == closed {
				continue next
			}
		}

		var assignee, labelString string
		if len(issue.Labels) > 0 {
			labelString = buildLabelString(issue.LabelDetails)
		}
		if issue.Assignee != nil { //nolint:staticcheck
			assignee = issue.Assignee.Username //nolint:staticcheck
		}

		boardIssues.WriteString(fmt.Sprintf("[white::b]%s\n%s[green:-:-]#%d[darkgray] - %s\n\n",
			issue.Title, labelString, issue.IID, assignee))
	}
	return boardIssues.String()
}
