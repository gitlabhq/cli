//go:build !integration

package view

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
)

func TestGetProjectBoardIssuesPaginates(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	call := 0
	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any(), gomock.Any()).
		Times(2).
		DoAndReturn(func(_ any, _ *gitlab.ListProjectIssuesOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
			call++
			if call == 1 {
				return []*gitlab.Issue{{IID: 1}}, &gitlab.Response{NextPage: 2}, nil
			}
			return []*gitlab.Issue{{IID: 2}}, &gitlab.Response{}, nil
		})

	issues, err := getProjectBoardIssues(
		testClient.Client,
		glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
		&issueBoardViewOptions{paginate: true},
	)

	require.NoError(t, err)
	require.Len(t, issues, 2)
	assert.Equal(t, int64(1), issues[0].IID)
	assert.Equal(t, int64(2), issues[1].IID)
}

func TestGetGroupBoardIssuesPaginates(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	call := 0
	testClient.MockIssues.EXPECT().
		ListGroupIssues(int64(42), gomock.Any(), gomock.Any()).
		Times(2).
		DoAndReturn(func(_ any, _ *gitlab.ListGroupIssuesOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
			call++
			if call == 1 {
				return []*gitlab.Issue{{IID: 1}}, &gitlab.Response{NextPage: 2}, nil
			}
			return []*gitlab.Issue{{IID: 2}}, &gitlab.Response{}, nil
		})

	issues, err := getGroupBoardIssues(testClient.Client, 42, &issueBoardViewOptions{paginate: true})

	require.NoError(t, err)
	require.Len(t, issues, 2)
	assert.Equal(t, int64(1), issues[0].IID)
	assert.Equal(t, int64(2), issues[1].IID)
}

func TestGetProjectBoardIssuesUsesSinglePageByDefault(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Issue{{IID: 1}}, &gitlab.Response{NextPage: 2}, nil)

	issues, err := getProjectBoardIssues(
		testClient.Client,
		glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
		&issueBoardViewOptions{},
	)

	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, int64(1), issues[0].IID)
}

func Test_issueBoardViewOptions_getListProjectIssueOptions(t *testing.T) {
	withLabelDetails := true
	labels := []string{"a", "b", "c"}
	milestone := "milestone"
	user := "user"
	state := "open"
	type fields struct {
		assignee  string
		labels    []string
		milestone string
		state     string
	}
	tests := []struct {
		name   string
		fields fields
		want   *gitlab.ListProjectIssuesOptions
	}{
		{
			name:   "return default values when passed empty options",
			fields: fields{},
			want: &gitlab.ListProjectIssuesOptions{
				WithLabelDetails: &withLabelDetails,
			},
		},
		{
			name: "return corresponding values when passing options",
			fields: fields{
				assignee:  user,
				labels:    labels,
				milestone: milestone,
				state:     state,
			},
			want: &gitlab.ListProjectIssuesOptions{
				WithLabelDetails: &withLabelDetails,
				AssigneeUsername: &user,
				Milestone:        &milestone,
				Labels:           &gitlab.LabelOptions{"a", "b", "c"},
				State:            &state,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &issueBoardViewOptions{
				assignee:  tt.fields.assignee,
				labels:    tt.fields.labels,
				milestone: tt.fields.milestone,
				state:     tt.fields.state,
			}
			got := opts.getListProjectIssueOptions()
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_issueBoardViewOptions_getListGroupIssueOptions(t *testing.T) {
	withLabelDetails := true
	labels := []string{"a", "b", "c"}
	milestone := "milestone"
	user := "user"
	state := "open"
	type fields struct {
		assignee  string
		labels    []string
		milestone string
		state     string
	}
	tests := []struct {
		name   string
		fields fields
		want   *gitlab.ListGroupIssuesOptions
	}{
		{
			name:   "return default values when passed empty options",
			fields: fields{},
			want: &gitlab.ListGroupIssuesOptions{
				WithLabelDetails: &withLabelDetails,
			},
		},
		{
			name: "return corresponding values when passing options",
			fields: fields{
				assignee:  user,
				labels:    labels,
				milestone: milestone,
				state:     state,
			},
			want: &gitlab.ListGroupIssuesOptions{
				WithLabelDetails: &withLabelDetails,
				AssigneeUsername: &user,
				Milestone:        &milestone,
				Labels:           &gitlab.LabelOptions{"a", "b", "c"},
				State:            &state,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &issueBoardViewOptions{
				assignee:  tt.fields.assignee,
				labels:    tt.fields.labels,
				milestone: tt.fields.milestone,
				state:     tt.fields.state,
			}
			got := opts.getListGroupIssueOptions()
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_buildLabelString(t *testing.T) {
	type args struct {
		labelDetails []*gitlab.LabelDetails
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "return empty string if no labeldetails are passed",
			args: args{[]*gitlab.LabelDetails{}},
			want: "",
		},
		{
			name: "return formatted string when labeldetails are passed",
			args: args{[]*gitlab.LabelDetails{
				{
					Color: "blue",
					Name:  "cold",
				},
				{
					Color: "red",
					Name:  "hot",
				},
			}},
			want: "[white:blue:-]cold[white:-:-] [white:red:-]hot[white:-:-]\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildLabelString(tt.args.labelDetails)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_mapBoardData(t *testing.T) {
	type args struct {
		projectIssueBoards      []*gitlab.IssueBoard
		projectGroupIssueBoards []*gitlab.GroupIssueBoard
	}
	type result struct {
		menuOptions  []string
		boardMetaMap map[string]boardMeta
	}
	tests := []struct {
		name string
		args args
		want result
	}{
		{
			name: "return empty map on empty inputs",
			args: args{
				projectIssueBoards:      []*gitlab.IssueBoard{},
				projectGroupIssueBoards: []*gitlab.GroupIssueBoard{},
			},
			want: result{
				menuOptions:  []string{},
				boardMetaMap: map[string]boardMeta{},
			},
		},
		{
			name: "return metadata map with input values",
			args: args{
				projectIssueBoards: []*gitlab.IssueBoard{
					{
						Name:    "projectBoard",
						Project: &gitlab.Project{Name: "project"},
						ID:      1,
					},
				},
				projectGroupIssueBoards: []*gitlab.GroupIssueBoard{
					{
						Name:  "groupBoard",
						ID:    2,
						Group: &gitlab.Group{Name: "group"},
					},
				},
			},
			want: result{
				menuOptions: []string{
					"groupBoard     (GROUP: group)",
					"projectBoard   (PROJECT: project)",
				},
				boardMetaMap: map[string]boardMeta{
					"projectBoard   (PROJECT: project)": {
						id:      1,
						name:    "projectBoard",
						project: &gitlab.Project{Name: "project"},
					},
					"groupBoard     (GROUP: group)": {
						id:    2,
						name:  "groupBoard",
						group: &gitlab.Group{Name: "group"},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			menuOptions, boardMetaMap := mapBoardData(
				tt.args.projectIssueBoards,
				tt.args.projectGroupIssueBoards,
			)
			assert.Equal(t, tt.want.menuOptions, menuOptions)
			assert.Equal(t, tt.want.boardMetaMap, boardMetaMap)
		})
	}
}

func Test_filterIssues(t *testing.T) {
	type args struct {
		boardLists []*gitlab.BoardList
		issues     []*gitlab.Issue
		targetList *gitlab.BoardList
		state      string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "return empty string on no matches",
			want: "",
		},
		{
			name: "filter out closed issues when targetList is not the 'closed' list",
			args: args{
				boardLists: []*gitlab.BoardList{{Label: &gitlab.Label{Name: "A"}}},
				issues:     []*gitlab.Issue{{Labels: []string{"A"}, State: "closed"}},
				targetList: &gitlab.BoardList{Label: &gitlab.Label{Name: "A"}},
			},
			want: "",
		},
		{
			name: "filter out issues not in the 'closed' state when populating the 'closed' list",
			args: args{
				boardLists: []*gitlab.BoardList{
					{Label: &gitlab.Label{Name: "Closed"}},
					{Label: &gitlab.Label{Name: "A"}},
				},
				issues:     []*gitlab.Issue{{Labels: []string{"A"}, State: "opened"}},
				targetList: &gitlab.BoardList{Label: &gitlab.Label{Name: "Closed"}},
				state:      closed,
			},
			want: "",
		},
		{
			name: "filter out issues labeled for other board lists when iterating over the 'open' list",
			args: args{
				boardLists: []*gitlab.BoardList{
					{Label: &gitlab.Label{Name: "Open"}},
					{Label: &gitlab.Label{Name: "A"}},
				},
				issues:     []*gitlab.Issue{{Labels: []string{"A"}, State: "opened"}},
				targetList: &gitlab.BoardList{Label: &gitlab.Label{Name: "Open"}},
				state:      opened,
			},
			want: "",
		},
		{
			name: "return formatted string on successful filter and match",
			args: args{
				boardLists: []*gitlab.BoardList{
					{Label: &gitlab.Label{Name: "A"}},
					{Label: &gitlab.Label{Name: "B"}},
					{Label: &gitlab.Label{Name: "C"}},
				},
				issues: []*gitlab.Issue{
					{
						Assignee:     &gitlab.IssueAssignee{Username: "user"},
						Labels:       []string{"A"},
						LabelDetails: []*gitlab.LabelDetails{{Name: "A", Color: "green"}},
						Title:        "Issue",
						IID:          1,
					},
				},
				targetList: &gitlab.BoardList{Label: &gitlab.Label{Name: "A"}},
			},
			want: "[white::b]Issue\n[white:green:-]A[white:-:-]\n[green:-:-]#1[darkgray] - user\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterIssues(
				tt.args.boardLists,
				tt.args.issues,
				tt.args.targetList,
				tt.args.state,
			)
			assert.Equal(t, tt.want, got)
		})
	}
}
