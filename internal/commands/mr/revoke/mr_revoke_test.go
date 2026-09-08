//go:build !integration

package revoke

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMrRevoke(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock getting the merge request
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test mr title",
				Description: "test mr description",
				State:       "opened",
			},
		}, nil, nil)

	// Mock unapproving the merge request
	tc.MockMergeRequestApprovals.EXPECT().
		UnapproveMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdRevoke, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	output, err := exec("123")

	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`
		- Revoking approval for merge request !123...
		✓ Merge request approval revoked.
		`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestMrRevokeCurrentBranch(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock listing merge requests for current branch
	tc.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return([]*gitlab.BasicMergeRequest{
			{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test mr title",
				Description: "test mr description",
				State:       "opened",
			},
		}, nil, nil)

	// Mock getting the merge request
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test mr title",
				Description: "test mr description",
				State:       "opened",
			},
		}, nil, nil)

	// Mock unapproving the merge request
	tc.MockMergeRequestApprovals.EXPECT().
		UnapproveMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdRevoke, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		cmdtest.WithBranch("current-branch"),
	)

	output, err := exec("")

	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`
		- Revoking approval for merge request !123...
		✓ Merge request approval revoked.
		`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestMrRevokeDraft(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock getting a draft merge request
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(456), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          456,
				IID:         456,
				ProjectID:   3,
				Title:       "Draft: test mr title",
				Description: "test mr description",
				State:       "opened",
				Draft:       true,
			},
		}, nil, nil)

	// Mock unapproving the draft merge request
	tc.MockMergeRequestApprovals.EXPECT().
		UnapproveMergeRequest("OWNER/REPO", int64(456), gomock.Any()).
		Return(nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdRevoke, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	output, err := exec("456")

	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`
		- Revoking approval for merge request !456...
		✓ Merge request approval revoked.
		`), output.String())
	assert.Empty(t, output.Stderr())
}
