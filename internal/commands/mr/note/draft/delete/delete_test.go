//go:build !integration

package delete

import (
	"errors"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDraftDelete(t *testing.T) {
	t.Parallel()

	t.Run("--yes deletes the pending comment", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc)
		tc.MockDraftNotes.EXPECT().
			DeleteDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any()).
			Return(nil, nil)

		output, err := setupExec(t, tc, false)(`1 501 --yes`)
		require.NoError(t, err)
		assert.Equal(t, "✓ Deleted pending review comment 501 from !1\n", output.String())
	})

	t.Run("a lone ID is the pending comment on the current branch's merge request", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockBranchMR(t, tc)
		mockGetDraft(tc)
		tc.MockDraftNotes.EXPECT().
			DeleteDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any()).
			Return(nil, nil)

		output, err := setupExec(t, tc, false, cmdtest.WithBranch("feature"))(`501 --yes`)
		require.NoError(t, err)
		assert.Equal(t, "✓ Deleted pending review comment 501 from !1\n", output.String())
	})

	t.Run("non-interactive without --yes fails before any API call", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)

		_, err := setupExec(t, tc, false)(`1 501`)
		require.ErrorContains(t, err, "--yes required when not running interactively")
	})

	t.Run("unknown ID", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		tc.MockDraftNotes.EXPECT().
			GetDraftNote("OWNER/REPO", int64(1), int64(999), gomock.Any()).
			Return(nil, nil, gitlab.ErrNotFound)

		_, err := setupExec(t, tc, false)(`1 999 --yes`)
		require.EqualError(t, err, "pending review comment 999 not found in merge request !1")
	})

	t.Run("lookup error other than not found is wrapped", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		tc.MockDraftNotes.EXPECT().
			GetDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any()).
			Return(nil, nil, errors.New("500 Internal Server Error"))

		_, err := setupExec(t, tc, false)(`1 501 --yes`)
		require.EqualError(t, err, "failed to get pending review comment 501: 500 Internal Server Error")
	})

	t.Run("non-numeric ID is rejected before any API call", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)

		_, err := setupExec(t, tc, false)(`1 abc12345 --yes`)
		require.EqualError(t, err, `invalid pending review comment ID "abc12345": must be a number`)
	})

	t.Run("delete error is wrapped", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc)
		tc.MockDraftNotes.EXPECT().
			DeleteDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any()).
			Return(nil, errors.New("boom"))

		_, err := setupExec(t, tc, false)(`1 501 --yes`)
		require.ErrorContains(t, err, "failed to delete pending review comment: boom")
	})
}

func TestDraftDelete_Prompt(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).

	t.Run("confirming deletes", func(t *testing.T) {
		tc := setupMR(t)
		mockGetDraft(tc)
		tc.MockDraftNotes.EXPECT().
			DeleteDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any()).
			Return(nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Delete this pending review comment?")).Do(ugh.Affirm)

		out, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))(`1 501`)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "Pending review comment 501: Pending draft")
		assert.Contains(t, out.String(), "✓ Deleted pending review comment 501 from !1")
	})

	t.Run("declining deletes nothing", func(t *testing.T) {
		tc := setupMR(t)
		mockGetDraft(tc)
		tc.MockDraftNotes.EXPECT().
			DeleteDraftNote(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Times(0)

		c := ugh.New(t)
		c.Expect(ugh.Confirm("Delete this pending review comment?")).Do(ugh.Reject)

		out, err := setupExec(t, tc, true, cmdtest.WithConsole(t, c))(`1 501`)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "Aborted.")
	})
}

func mockGetDraft(tc *gitlabtesting.TestClient) {
	tc.MockDraftNotes.EXPECT().
		GetDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any()).
		Return(&gitlab.DraftNote{ID: 501, Note: "Pending draft"}, nil, nil)
}

func setupMR(t *testing.T) *gitlabtesting.TestClient {
	t.Helper()
	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 1, IID: 1}}, nil, nil)
	return tc
}

// mockBranchMR resolves branch "feature" to merge request !1.
func mockBranchMR(t *testing.T, tc *gitlabtesting.TestClient) {
	t.Helper()
	tc.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.ListProjectMergeRequestsOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "feature", *opts.SourceBranch)
			return []*gitlab.BasicMergeRequest{{ID: 1, IID: 1}}, nil, nil
		})
}

func setupExec(t *testing.T, tc *gitlabtesting.TestClient, isTTY bool, opts ...cmdtest.FactoryOption) cmdtest.CmdExecFunc {
	t.Helper()
	opts = append([]cmdtest.FactoryOption{
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	}, opts...)
	return cmdtest.SetupCmdForTest(t, NewCmd, isTTY, opts...)
}
