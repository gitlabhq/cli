//go:build !integration

package unsubscribe

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

func Test_IssuableUnsubscribe(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	type testCase struct {
		name       string
		iid        int
		issueType  issuable.IssueType
		wantOutput string
		wantErr    bool
		setupMock  func(tc *gitlabtesting.TestClient)
	}

	createdAt, _ := time.Parse(time.RFC3339, "2023-05-02T10:51:26.371Z")

	testCases := []testCase{
		{
			name:      "issue_unsubscribe",
			iid:       1,
			issueType: issuable.TypeIssue,
			wantOutput: heredoc.Doc(`
				- Unsubscribing from issue #1 in OWNER/REPO
				✓ Unsubscribed
				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         1,
						IID:        1,
						Title:      "test issue",
						Subscribed: true,
						IssueType:  new("issue"),
						CreatedAt:  &createdAt,
					}, nil, nil)
				tc.MockIssues.EXPECT().
					UnsubscribeFromIssue("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         1,
						IID:        1,
						Subscribed: false,
						IssueType:  new("issue"),
						CreatedAt:  &createdAt,
					}, nil, nil)
			},
		},
		{
			name:      "incident_unsubscribe",
			iid:       2,
			issueType: issuable.TypeIncident,
			wantOutput: heredoc.Doc(`
				- Unsubscribing from incident #2 in OWNER/REPO
				✓ Unsubscribed
				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         2,
						IID:        2,
						Title:      "test incident",
						Subscribed: true,
						IssueType:  new("incident"),
						CreatedAt:  &createdAt,
					}, nil, nil)
				tc.MockIssues.EXPECT().
					UnsubscribeFromIssue("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         2,
						IID:        2,
						Subscribed: false,
						IssueType:  new("incident"),
						CreatedAt:  &createdAt,
					}, nil, nil)
			},
		},
		{
			name:      "incident_unsubscribe_using_issue_command",
			iid:       2,
			issueType: issuable.TypeIssue,
			wantOutput: heredoc.Doc(`
				- Unsubscribing from issue #2 in OWNER/REPO
				✓ Unsubscribed
				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         2,
						IID:        2,
						Title:      "test incident",
						Subscribed: true,
						IssueType:  new("incident"),
						CreatedAt:  &createdAt,
					}, nil, nil)
				tc.MockIssues.EXPECT().
					UnsubscribeFromIssue("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         2,
						IID:        2,
						Subscribed: false,
						IssueType:  new("incident"),
						CreatedAt:  &createdAt,
					}, nil, nil)
			},
		},
		{
			name:       "issue_unsubscribe_using_incident_command",
			iid:        1,
			issueType:  issuable.TypeIncident,
			wantOutput: "Incident not found, but an issue with the provided ID exists. Run `glab issue unsubscribe <id>` to unsubscribe.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         1,
						IID:        1,
						Title:      "test issue",
						Subscribed: true,
						IssueType:  new("issue"),
						CreatedAt:  &createdAt,
					}, nil, nil)
			},
		},
		{
			name:      "issue_unsubscribe_from_non_subscribed_issue",
			iid:       3,
			issueType: issuable.TypeIssue,
			wantOutput: heredoc.Doc(`
				- Unsubscribing from issue #3 in OWNER/REPO
				x You are not subscribed to this issue.
				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(3), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         3,
						IID:        3,
						Title:      "test issue",
						Subscribed: false,
						IssueType:  new("issue"),
						CreatedAt:  &createdAt,
					}, nil, nil)
				notModifiedResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotModified}}
				tc.MockIssues.EXPECT().
					UnsubscribeFromIssue("OWNER/REPO", int64(3), gomock.Any()).
					Return(nil, notModifiedResponse, fmt.Errorf("304 Not Modified"))
			},
		},
		{
			name:      "incident_unsubscribe_from_non_subscribed_incident",
			iid:       3,
			issueType: issuable.TypeIncident,
			wantOutput: heredoc.Doc(`
				- Unsubscribing from incident #3 in OWNER/REPO
				x You are not subscribed to this incident.
				`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(3), gomock.Any()).
					Return(&gitlab.Issue{
						ID:         3,
						IID:        3,
						Title:      "test incident",
						Subscribed: false,
						IssueType:  new("incident"),
						CreatedAt:  &createdAt,
					}, nil, nil)
				notModifiedResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotModified}}
				tc.MockIssues.EXPECT().
					UnsubscribeFromIssue("OWNER/REPO", int64(3), gomock.Any()).
					Return(nil, notModifiedResponse, fmt.Errorf("304 Not Modified"))
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
				return NewCmdUnsubscribe(f, tc.issueType)
			}

			exec := cmdtest.SetupCmdForTest(
				t,
				cmdFunc,
				true, // TTY mode for unsubscribe output
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
			assert.Contains(t, out.String(), tc.wantOutput)
			assert.Empty(t, out.Stderr())
		})
	}
}
