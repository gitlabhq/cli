//go:build !integration

package list

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	timelogapi "gitlab.com/gitlab-org/cli/internal/commands/timelog/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// GroupOverride reads the package-level viper in cmdutils, so these tests do
// not call t.Parallel.

func setupCmd(t *testing.T, tc *gitlabtesting.TestClient) cmdtest.CmdExecFunc {
	t.Helper()
	return cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)
}

// expectPage expects one timelogs query, captures it, and answers with conn
// under data.project, or under data.group when group is true.
func expectPage(tc *gitlabtesting.TestClient, group bool, conn *timelogapi.TimelogConnection, captured *gitlab.GraphQLQuery) *gitlabtesting.MockGraphQLInterfaceDoCall {
	return tc.MockGraphQL.EXPECT().
		Do(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(query gitlab.GraphQLQuery, response any, _ ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
			if captured != nil {
				*captured = query
			}
			resp, ok := response.(*timelogapi.TimelogsResponse)
			if !ok {
				return nil, fmt.Errorf("unexpected response type %T", response)
			}
			if group {
				resp.Data.Group = &timelogapi.Namespace{Timelogs: conn}
			} else {
				resp.Data.Project = &timelogapi.Namespace{Timelogs: conn}
			}
			return &gitlab.Response{}, nil
		})
}

func spentAt(day int) time.Time {
	return time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC)
}

func TestListProject(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	var query gitlab.GraphQLQuery
	expectPage(tc, false, &timelogapi.TimelogConnection{
		Nodes: []timelogapi.Timelog{
			{
				SpentAt:   spentAt(14),
				TimeSpent: 5400,
				Summary:   "Report command",
				User:      timelogapi.User{Username: "alice"},
				Project:   timelogapi.Project{FullPath: "OWNER/REPO"},
				Issue:     &timelogapi.Issuable{IID: "969", Title: "Handle time tracking"},
			},
			{
				SpentAt:      spentAt(13),
				TimeSpent:    2700,
				User:         timelogapi.User{Username: "bob"},
				Project:      timelogapi.Project{FullPath: "OWNER/REPO"},
				MergeRequest: &timelogapi.Issuable{IID: "4092", Title: "feat: time tracking"},
			},
			{
				SpentAt:   spentAt(12),
				TimeSpent: -900,
				Summary:   "Logged twice",
				User:      timelogapi.User{Username: "bob"},
				Project:   timelogapi.Project{FullPath: "OWNER/REPO"},
				Issue:     &timelogapi.Issuable{IID: "969", Title: "Handle time tracking"},
			},
		},
	}, &query)

	out, err := setupCmd(t, tc)("")
	require.NoError(t, err)

	assert.Contains(t, query.Query, "project(fullPath: $fullPath)")
	assert.Equal(t, map[string]any{"fullPath": "OWNER/REPO"}, query.Variables)

	expected := heredoc.Doc(`
		DATE	USER	TIME	ITEM	TITLE	SUMMARY
		2026-10-14	alice	1h 30m	#969	Handle time tracking	Report command
		2026-10-13	bob	45m	!4092	feat: time tracking	-
		2026-10-12	bob	-15m	#969	Handle time tracking	Logged twice

		Total: 2h (3 time logs)
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestListGroup(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	var query gitlab.GraphQLQuery
	expectPage(tc, true, &timelogapi.TimelogConnection{
		Nodes: []timelogapi.Timelog{
			{
				SpentAt:   spentAt(14),
				TimeSpent: 3600,
				User:      timelogapi.User{Username: "alice"},
				Project:   timelogapi.Project{FullPath: "my-group/sub/app"},
				Issue:     &timelogapi.Issuable{IID: "7", Title: "Fix login"},
			},
			{
				SpentAt:      spentAt(13),
				TimeSpent:    8 * 3600,
				User:         timelogapi.User{Username: "bob"},
				Project:      timelogapi.Project{FullPath: "my-group/api"},
				MergeRequest: &timelogapi.Issuable{IID: "12", Title: "Add endpoint"},
			},
		},
	}, &query)

	out, err := setupCmd(t, tc)("--group my-group")
	require.NoError(t, err)

	assert.Contains(t, query.Query, "group(fullPath: $fullPath)")
	assert.Equal(t, map[string]any{"fullPath": "my-group"}, query.Variables)

	expected := heredoc.Doc(`
		DATE	USER	TIME	ITEM	TITLE	SUMMARY
		2026-10-14	alice	1h	my-group/sub/app#7	Fix login	-
		2026-10-13	bob	1d	my-group/api!12	Add endpoint	-

		Total: 1d 1h (2 time logs)
	`)
	assert.Equal(t, expected, out.OutBuf.String())
}

func TestListFilters(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	var query gitlab.GraphQLQuery
	expectPage(tc, false, &timelogapi.TimelogConnection{}, &query)

	_, err := setupCmd(t, tc)("--user alice --start-date 2026-10-01 --end-date 2026-10-14")
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"fullPath":  "OWNER/REPO",
		"username":  "alice",
		"startDate": "2026-10-01",
		"endDate":   "2026-10-14",
	}, query.Variables)
}

func TestListUsernameWithLeadingAt(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	var query gitlab.GraphQLQuery
	expectPage(tc, false, &timelogapi.TimelogConnection{}, &query)

	_, err := setupCmd(t, tc)("--user @alice")
	require.NoError(t, err)

	assert.Equal(t, "alice", query.Variables["username"])
}

func TestListDatesInUTC(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("UTC+10", 10*60*60)
	t.Cleanup(func() { time.Local = local })

	tc := gitlabtesting.NewTestClient(t)
	expectPage(tc, false, &timelogapi.TimelogConnection{
		Nodes: []timelogapi.Timelog{
			{
				SpentAt:   time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC),
				TimeSpent: 3600,
				User:      timelogapi.User{Username: "alice"},
				Issue:     &timelogapi.Issuable{IID: "1", Title: "First"},
			},
		},
	}, nil)

	out, err := setupCmd(t, tc)("")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "2026-10-14\talice")
}

func TestListCurrentUser(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	var query gitlab.GraphQLQuery
	gomock.InOrder(
		tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(&gitlab.User{Username: "alice"}, nil, nil),
		expectPage(tc, false, &timelogapi.TimelogConnection{}, &query),
	)

	_, err := setupCmd(t, tc)("--user @me")
	require.NoError(t, err)

	assert.Equal(t, "alice", query.Variables["username"])
}

func TestListCurrentUserError(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(nil, nil, errors.New("401 Unauthorized"))

	_, err := setupCmd(t, tc)("--user @me")
	require.ErrorContains(t, err, "failed to get the current user")
}

func TestListPaginates(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	var first, second gitlab.GraphQLQuery
	// GitLab drops timelogs the user cannot see from a page, so a page can be
	// empty while more pages follow.
	gomock.InOrder(
		expectPage(tc, false, &timelogapi.TimelogConnection{
			PageInfo: timelogapi.PageInfo{HasNextPage: true, EndCursor: "cursor-1"},
		}, &first),
		expectPage(tc, false, &timelogapi.TimelogConnection{
			Nodes: []timelogapi.Timelog{
				{
					SpentAt:   spentAt(1),
					TimeSpent: 600,
					User:      timelogapi.User{Username: "alice"},
					Issue:     &timelogapi.Issuable{IID: "1", Title: "First"},
				},
			},
		}, &second),
	)

	out, err := setupCmd(t, tc)("")
	require.NoError(t, err)

	assert.NotContains(t, first.Variables, "after")
	assert.Equal(t, "cursor-1", second.Variables["after"])
	assert.Contains(t, out.OutBuf.String(), "Total: 10m (1 time log)")
}

func TestListEmpty(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	expectPage(tc, false, &timelogapi.TimelogConnection{}, nil)

	out, err := setupCmd(t, tc)("")
	require.NoError(t, err)

	assert.Equal(t, "No time logged in OWNER/REPO.\n", out.OutBuf.String())
}

func TestListJSON(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	expectPage(tc, false, &timelogapi.TimelogConnection{
		Nodes: []timelogapi.Timelog{
			{
				SpentAt:   spentAt(14),
				TimeSpent: 5400,
				Summary:   "Report command",
				User:      timelogapi.User{Username: "alice"},
				Project:   timelogapi.Project{FullPath: "OWNER/REPO"},
				Issue:     &timelogapi.Issuable{IID: "969", Title: "Handle time tracking", WebURL: "https://gitlab.com/OWNER/REPO/-/issues/969"},
			},
			{
				SpentAt:      spentAt(13),
				TimeSpent:    -900,
				User:         timelogapi.User{Username: "bob"},
				Project:      timelogapi.Project{FullPath: "OWNER/REPO"},
				MergeRequest: &timelogapi.Issuable{IID: "4092", Title: "feat: time tracking"},
			},
		},
	}, nil)

	out, err := setupCmd(t, tc)("--output json")
	require.NoError(t, err)

	var timelogs []timelogapi.Timelog
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &timelogs))
	require.Len(t, timelogs, 2)

	assert.Equal(t, spentAt(14), timelogs[0].SpentAt)
	assert.Equal(t, int64(5400), timelogs[0].TimeSpent)
	assert.Equal(t, "Report command", timelogs[0].Summary)
	assert.Equal(t, "alice", timelogs[0].User.Username)
	require.NotNil(t, timelogs[0].Issue)
	assert.Equal(t, "https://gitlab.com/OWNER/REPO/-/issues/969", timelogs[0].Issue.WebURL)
	assert.Nil(t, timelogs[0].MergeRequest)

	assert.Equal(t, int64(-900), timelogs[1].TimeSpent)
	require.NotNil(t, timelogs[1].MergeRequest)
	assert.Equal(t, "4092", timelogs[1].MergeRequest.IID)
	assert.Nil(t, timelogs[1].Issue)
}

func TestListJSONEmpty(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	expectPage(tc, false, &timelogapi.TimelogConnection{}, nil)

	out, err := setupCmd(t, tc)("--output json")
	require.NoError(t, err)

	assert.JSONEq(t, "[]", out.OutBuf.String())
}

func TestListGraphQLErrors(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockGraphQL.EXPECT().
		Do(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ gitlab.GraphQLQuery, response any, _ ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
			resp, ok := response.(*timelogapi.TimelogsResponse)
			if !ok {
				return nil, fmt.Errorf("unexpected response type %T", response)
			}
			require.NoError(t, json.Unmarshal([]byte(`{
				"errors": [{"message": "Start argument must be before End argument"}],
				"data": {"project": {"timelogs": null}}
			}`), resp))
			return &gitlab.Response{}, nil
		})

	_, err := setupCmd(t, tc)("")
	require.EqualError(t, err, "failed to list timelogs: Start argument must be before End argument")
}

func TestListNotFound(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockGraphQL.EXPECT().
		Do(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&gitlab.Response{}, nil)

	_, err := setupCmd(t, tc)("--group missing")
	require.EqualError(t, err, "group not found: missing")
}

func TestListRequestError(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockGraphQL.EXPECT().
		Do(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused"))

	_, err := setupCmd(t, tc)("")
	require.EqualError(t, err, "failed to list timelogs: connection refused")
}

func TestListNoRepository(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepoError(errors.New("not a git repository")),
	)

	_, err := exec("")
	require.EqualError(t, err, "not a git repository")
}

func TestListFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "end date before start date",
			args:    "--start-date 2026-10-14 --end-date 2026-10-01",
			wantErr: "invalid date range: --end-date is before --start-date",
		},
		{
			name:    "date not in YYYY-MM-DD format",
			args:    "--start-date 14/10/2026",
			wantErr: "invalid argument \"14/10/2026\" for \"--start-date\" flag",
		},
		{
			name:    "group and repo together",
			args:    "--group my-group --repo OWNER/REPO",
			wantErr: "if any flags in the group [group repo] are set none of the others can be",
		},
		{
			name:    "positional argument",
			args:    "extra",
			wantErr: "unknown command \"extra\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)

			_, err := setupCmd(t, tc)(tt.args)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
