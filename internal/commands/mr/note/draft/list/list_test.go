//go:build !integration

package list

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDraftList(t *testing.T) {
	t.Parallel()

	t.Run("prints pending comments", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDrafts(tc,
			&gitlab.DraftNote{ID: 501, Note: "General draft", Position: &gitlab.NotePosition{}},
			&gitlab.DraftNote{ID: 502, Note: "Diff draft", Position: &gitlab.NotePosition{NewPath: "main.go", NewLine: 42}},
		)

		output, err := setupExec(t, tc)(`1`)
		require.NoError(t, err)
		assert.Equal(t, "[draft #501]\n General draft\n\n[draft #502] on main.go:42\n Diff draft\n\n", output.String())
	})

	t.Run("--file keeps only pending comments on that file", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDrafts(tc,
			&gitlab.DraftNote{ID: 501, Note: "General draft"},
			&gitlab.DraftNote{ID: 502, Note: "On main", Position: &gitlab.NotePosition{NewPath: "main.go", NewLine: 42}},
			&gitlab.DraftNote{ID: 503, Note: "On other", Position: &gitlab.NotePosition{NewPath: "other.go", NewLine: 7}},
			&gitlab.DraftNote{ID: 504, Note: "On removed main", Position: &gitlab.NotePosition{OldPath: "main.go", OldLine: 3}},
		)

		output, err := setupExec(t, tc)(`1 --file main.go`)
		require.NoError(t, err)
		assert.Equal(t, "[draft #502] on main.go:42\n On main\n\n[draft #504] on main.go:3\n On removed main\n\n", output.String())
	})

	t.Run("JSON output returns the pending comment objects", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDrafts(tc,
			&gitlab.DraftNote{ID: 501, Note: "General draft"},
			&gitlab.DraftNote{ID: 502, Note: "Reply", DiscussionID: "abc123"},
		)

		output, err := setupExec(t, tc)(`1 -F json`)
		require.NoError(t, err)

		var got []struct {
			ID           int64  `json:"id"`
			Note         string `json:"note"`
			DiscussionID string `json:"discussion_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(output.String()), &got))
		require.Len(t, got, 2)
		assert.Equal(t, int64(501), got[0].ID)
		assert.Equal(t, "General draft", got[0].Note)
		assert.Equal(t, int64(502), got[1].ID)
		assert.Equal(t, "abc123", got[1].DiscussionID)
	})

	t.Run("JSON output honours --file", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDrafts(tc,
			&gitlab.DraftNote{ID: 501, Note: "General draft"},
			&gitlab.DraftNote{ID: 502, Note: "On main", Position: &gitlab.NotePosition{NewPath: "main.go", NewLine: 42}},
			&gitlab.DraftNote{ID: 503, Note: "On other", Position: &gitlab.NotePosition{NewPath: "other.go", NewLine: 7}},
		)

		output, err := setupExec(t, tc)(`1 --file main.go -F json`)
		require.NoError(t, err)

		var got []struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(output.String()), &got))
		require.Len(t, got, 1)
		assert.Equal(t, int64(502), got[0].ID)
	})

	t.Run("JSON output with no pending comments is an empty array", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDrafts(tc)

		output, err := setupExec(t, tc)(`1 -F json`)
		require.NoError(t, err)
		assert.JSONEq(t, "[]", output.String())
	})

	t.Run("no pending comments", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDrafts(tc)

		output, err := setupExec(t, tc)(`1`)
		require.NoError(t, err)
		assert.Equal(t, "No pending review comments found.\n", output.String())
	})

	t.Run("collects every page", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		gomock.InOrder(
			tc.MockDraftNotes.EXPECT().
				ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
				Return([]*gitlab.DraftNote{{ID: 501, Note: "first"}}, &gitlab.Response{NextPage: 2}, nil),
			tc.MockDraftNotes.EXPECT().
				ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
				Return([]*gitlab.DraftNote{{ID: 502, Note: "second"}}, &gitlab.Response{}, nil),
		)

		output, err := setupExec(t, tc)(`1`)
		require.NoError(t, err)
		assert.Equal(t, "[draft #501]\n first\n\n[draft #502]\n second\n\n", output.String())
	})

	t.Run("API error is wrapped", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		tc.MockDraftNotes.EXPECT().
			ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(nil, nil, errors.New("boom"))

		_, err := setupExec(t, tc)(`1`)
		require.ErrorContains(t, err, "failed to list pending review comments: boom")
	})
}

func mockDrafts(tc *gitlabtesting.TestClient, drafts ...*gitlab.DraftNote) {
	tc.MockDraftNotes.EXPECT().
		ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		Return(drafts, &gitlab.Response{}, nil)
}

func setupMR(t *testing.T) *gitlabtesting.TestClient {
	t.Helper()
	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 1, IID: 1}}, nil, nil)
	return tc
}

func setupExec(t *testing.T, tc *gitlabtesting.TestClient) cmdtest.CmdExecFunc {
	t.Helper()
	return cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)
}
