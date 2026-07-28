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

			// list the groups that are ancestors to project:
			// https://docs.gitlab.com/api/projects/#list-groups
			projectGroups, _, err := client.Projects.ListProjectsGroups(project.ID, &gitlab.ListProjectGroupOptions{})
			if err != nil {
				return err
			}

			// get issue boards related to project and parent groups
			// https://docs.gitlab.com/api/group_boards/#list-group-issue-board-lists
			projectIssueBoards, err := getProjectIssueBoards(client, repo)
			if err != nil {
				return fmt.Errorf("getting project issue boards: %w", err)
			}

			projectGroupIssueBoards, err := getGroupIssueBoards(projectGroups, client)
			if err != nil {
				return fmt.Errorf("getting project issue boards: %w", err)
			}

			// prompt user to select issue board
			menuOptions, boardMetaMap := mapBoardData(projectIssueBoards, projectGroupIssueBoards)
			selection, err := selectBoard(cmd.Context(), f.IO(), menuOptions)
			if err != nil {
				return fmt.Errorf("selecting issue board: %w", err)
			}
			selectedBoard := boardMetaMap[selection]

			boardLists, err := getBoardLists(client, selectedBoard, repo)
			if err != nil {
				return fmt.Errorf("getting issue board lists: %w", err)
			}

			root := tview.NewFlex()
			root.SetBackgroundColor(tcell.ColorDefault)
			for _, l := range boardLists {
				opts.state = ""
				var boardIssues, listTitle, listColor string

				if l.Label == nil {
					continue
				}

				if l.Label != nil {
					listTitle = l.Label.Name
					listColor = l.Label.Color
				}

				// automatically request using state for default "open" and "closed" lists
				// this is required as these lists aren't returned with the board lists api call
				switch l.Label.Name {
				case "Closed":
					opts.state = closed
				case "Open":
					opts.state = opened
				}

				issues := []*gitlab.Issue{}
				if selectedBoard.group != nil {
					groupID := selectedBoard.group.ID
					issues, err = getGroupBoardIssues(client, groupID, opts)
					if err != nil {
						return fmt.Errorf("getting issue board lists: %w", err)
					}
				}

				if selectedBoard.group == nil {
					issues, err = getProjectBoardIssues(client, repo, opts)
					if err != nil {
						return fmt.Errorf("getting issue board lists: %w", err)
					}
				}

				boardIssues = filterIssues(boardLists, issues, l, opts)
				bx := tview.NewTextView()
				bx.
					SetDynamicColors(true).
					SetText(boardIssues).
					SetWrap(true).
					SetBackgroundColor(tcell.ColorDefault).
					SetBorder(true).
					SetTitle(listTitle).
					SetTitleColor(tcell.GetColor(listColor))
				root.AddItem(bx, 0, 1, false)
			}

			// format table title
			caser := cases.Title(language.English)
			var boardType, boardContext string
			if selectedBoard.group != nil {
				boardType = caser.String("group")
				boardContext = project.Namespace.Name
			} else {
				boardType = caser.String("project")
				boardContext = project.NameWithNamespace
			}
			root.SetBorderPadding(1, 1, 2, 2).SetBorder(true).SetTitle(
				fmt.Sprintf(" %s • %s ", caser.String(boardType+" issue board"), boardContext),
			)

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
	projectGroupIssueBoards := []*gitlab.GroupIssueBoard{}
	for _, projectGroup := range projectGroups {
		groupIssueBoards, _, err := gitlabClient.GroupIssueBoards.ListGroupIssueBoards(projectGroup.ID, &gitlab.ListGroupIssueBoardsOptions{})
		if err != nil {
			return nil, fmt.Errorf("retrieving issue board: %w", err)
		}
		projectGroupIssueBoards = append(groupIssueBoards, projectGroupIssueBoards...)
	}
	return projectGroupIssueBoards, nil
}

func getBoardLists(apiClient *gitlab.Client, board boardMeta, repo glrepo.Interface) ([]*gitlab.BoardList, error) {
	boardLists := []*gitlab.BoardList{}
	var err error

	if board.group != nil {
		boardLists, _, err = apiClient.GroupIssueBoards.ListGroupIssueBoardLists(board.group.ID, board.id, &gitlab.ListGroupIssueBoardListsOptions{})
		if err != nil {
			return nil, err
		}
	}

	if board.group == nil {
		boardLists, _, err = apiClient.Boards.GetIssueBoardLists(repo.FullName(), board.id, &gitlab.GetIssueBoardListsOptions{})
		if err != nil {
			return nil, err
		}
	}

	// add empty 'opened' and 'closed' lists before and after fetched lists
	// these are used later when reading the issues into the table view
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
	boardLists = append(boardLists, closed)
	return boardLists, nil
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

// filterIssues scans through the issues passed to it, filtering for the ones that belong in targetList
// This function returns a string representation of the issues for targetList which will be displayed in the table view
func filterIssues(
	boardLists []*gitlab.BoardList,
	issues []*gitlab.Issue,
	targetList *gitlab.BoardList,
	opts *issueBoardViewOptions,
) string {
	var boardIssues strings.Builder
next:
	for _, issue := range issues {
		switch opts.state {
		// skip all issues that are not in the "closed" state for the "closed" list
		case closed:
			if issue.State != closed {
				continue next
			}
		// skip issues labeled for other board lists when populating the "open" list
		case opened:
			for _, boardList := range boardLists {
				for _, issueLabel := range issue.Labels {
					if issueLabel == boardList.Label.Name {
						continue next
					}
				}
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
