//go:build !integration

package approvers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMrApprovers(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testMR := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:          123,
			IID:         123,
			ProjectID:   3,
			Title:       "test mr title",
			Description: "test mr description",
			State:       "opened",
		},
	}

	approvalState := &gitlab.MergeRequestApprovalState{
		ApprovalRulesOverwritten: true,
		Rules: []*gitlab.MergeRequestApprovalRule{
			{
				ID:       239,
				Name:     "All Members",
				RuleType: "any_approver",
				EligibleApprovers: []*gitlab.BasicUser{
					{
						ID:       1,
						Username: "approver_1",
						Name:     "Abc Approver",
						State:    "active",
					},
					{
						ID:       6,
						Username: "approver_2",
						Name:     "Bar Approver",
						State:    "active",
					},
				},
				ApprovalsRequired: 1,
				ApprovedBy: []*gitlab.BasicUser{
					{
						ID:       1232,
						Username: "foo_reviewer",
						Name:     "Foo Reviewer",
						State:    "active",
					},
				},
				Approved: true,
			},
		},
	}

	testCases := []testCase{
		{
			name: "List approvers by MR ID",
			cli:  "123",
			// Note: trailing tabs are added by the table renderer
			expectedOut: "\nListing merge request !123 eligible approvers:\n" +
				"Approval rules overwritten.\n" +
				"Rule \"All Members\" sufficient approvals (1/1 required):\n" +
				"Name\tUsername\tApproved\n" +
				"Abc Approver\tapprover_1\t-\t\n" +
				"Bar Approver\tapprover_2\t-\t\n" +
				"Foo Reviewer\tfoo_reviewer\t👍\t\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(testMR, nil, nil)
				tc.MockMergeRequestApprovals.EXPECT().
					GetApprovalState("OWNER/REPO", int64(123), gomock.Any()).
					Return(approvalState, nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdApprovers,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestMrApprovers_JSON(t *testing.T) {
	t.Parallel()

	testMR := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:          123,
			IID:         123,
			ProjectID:   3,
			Title:       "test mr title",
			Description: "test mr description",
			State:       "opened",
		},
	}

	approvalState := &gitlab.MergeRequestApprovalState{
		ApprovalRulesOverwritten: true,
		Rules: []*gitlab.MergeRequestApprovalRule{
			{
				ID:       239,
				Name:     "All Members",
				RuleType: "any_approver",
				EligibleApprovers: []*gitlab.BasicUser{
					{
						ID:       1,
						Username: "approver_1",
						Name:     "Abc Approver",
						State:    "active",
					},
				},
				ApprovalsRequired: 1,
				ApprovedBy: []*gitlab.BasicUser{
					{
						ID:       1232,
						Username: "foo_reviewer",
						Name:     "Foo Reviewer",
						State:    "active",
					},
				},
				Approved: true,
			},
		},
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(testMR, nil, nil)
	testClient.MockMergeRequestApprovals.EXPECT().
		GetApprovalState("OWNER/REPO", int64(123), gomock.Any()).
		Return(approvalState, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdApprovers,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("123 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"approval_rules_overwritten":true`)
	assert.Contains(t, out.String(), `"name":"All Members"`)
	assert.Contains(t, out.String(), `"username":"approver_1"`)
	assert.Empty(t, out.Stderr())
}
