//go:build !integration

package reopen

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_IssuableReopen(t *testing.T) {
	type testCase struct {
		name       string
		iid        int
		issueType  issuable.IssueType
		wantOutput string
		wantErr    bool
		setupMock  func(tc *gitlabtesting.TestClient)
	}

	createdAt, _ := time.Parse(time.RFC3339, "2023-04-05T10:51:26.371Z")

	testCases := []testCase{
		{
			name:      "issue_reopen",
			iid:       1,
			issueType: issuable.TypeIssue,
			wantOutput: heredoc.Doc(`
				- Reopening issue...
				✓ Reopened issue #1.

				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        1,
						IID:       1,
						Title:     "test issue",
						State:     "closed",
						IssueType: new("issue"),
						CreatedAt: &createdAt,
					}, nil, nil)
				tc.MockIssues.EXPECT().
					UpdateIssue("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        1,
						IID:       1,
						State:     "open",
						IssueType: new("issue"),
						CreatedAt: &createdAt,
					}, nil, nil)
			},
		},
		{
			name:      "incident_reopen",
			iid:       2,
			issueType: issuable.TypeIncident,
			wantOutput: heredoc.Doc(`
				- Reopening incident...
				✓ Reopened incident #2.

				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        2,
						IID:       2,
						Title:     "test incident",
						State:     "closed",
						IssueType: new("incident"),
						CreatedAt: &createdAt,
					}, nil, nil)
				tc.MockIssues.EXPECT().
					UpdateIssue("OWNER/REPO", int64(2), gomock.Any(), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        2,
						IID:       2,
						State:     "opened",
						IssueType: new("incident"),
						CreatedAt: &createdAt,
					}, nil, nil)
			},
		},
		{
			name:      "incident_reopen_using_issue_command",
			iid:       2,
			issueType: issuable.TypeIssue,
			wantOutput: heredoc.Doc(`
				- Reopening issue...
				✓ Reopened issue #2.

				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        2,
						IID:       2,
						Title:     "test incident",
						State:     "closed",
						IssueType: new("incident"),
						CreatedAt: &createdAt,
					}, nil, nil)
				tc.MockIssues.EXPECT().
					UpdateIssue("OWNER/REPO", int64(2), gomock.Any(), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        2,
						IID:       2,
						State:     "opened",
						IssueType: new("incident"),
						CreatedAt: &createdAt,
					}, nil, nil)
			},
		},
		{
			name:       "issue_reopen_using_incident_command",
			iid:        1,
			issueType:  issuable.TypeIncident,
			wantOutput: "Incident not found, but an issue with the provided ID exists. Run `glab issue reopen <id>` to reopen.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        1,
						IID:       1,
						Title:     "test issue",
						State:     "closed",
						IssueType: new("issue"),
						CreatedAt: &createdAt,
					}, nil, nil)
			},
		},
		{
			name:       "issue_not_found",
			iid:        404,
			issueType:  issuable.TypeIssue,
			wantOutput: "404 Not Found",
			wantErr:    true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(404), gomock.Any()).
					Return(nil, notFoundResponse, fmt.Errorf("404 Not Found"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			cmdFunc := func(f cmdutils.Factory) *cobra.Command {
				return NewCmdReopen(f, tc.issueType)
			}

			exec := cmdtest.SetupCmdForTest(
				t,
				cmdFunc,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(fmt.Sprint(tc.iid))

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantOutput)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOutput, out.String())
			assert.Empty(t, out.Stderr())
		})
	}
}
