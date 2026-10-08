//go:build !integration

package publish

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

const mr1AuthorID int64 = 42

func TestDraftPublish(t *testing.T) {
	t.Parallel()

	t.Run("publishes with summary note, internal, and reviewer state", func(t *testing.T) {
		t.Parallel()

		testClient := setupMR(t)
		mockDraftList(t, testClient, 2)

		testClient.MockDraftNotes.EXPECT().
			PublishAllDraftNotesWithOptions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(pid any, mrIID int64, opts *gitlab.PublishAllDraftNotesOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
				require.NotNil(t, opts.Note)
				assert.Equal(t, "Overall LGTM", *opts.Note)
				require.NotNil(t, opts.Internal)
				assert.True(t, *opts.Internal)
				require.NotNil(t, opts.ReviewerState)
				assert.Equal(t, "requested_changes", *opts.ReviewerState)
				return nil, nil
			})

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y -m "Overall LGTM" --internal --reviewer-state requested_changes`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Published 2 pending review comments.")
		assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1")
	})

	t.Run("bare publish sends no body fields", func(t *testing.T) {
		t.Parallel()

		testClient := setupMR(t)
		mockDraftList(t, testClient, 1)

		testClient.MockDraftNotes.EXPECT().
			PublishAllDraftNotesWithOptions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(pid any, mrIID int64, opts *gitlab.PublishAllDraftNotesOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
				assert.Nil(t, opts.Note)
				assert.Nil(t, opts.Internal)
				assert.Nil(t, opts.ReviewerState)
				return nil, nil
			})

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Published 1 pending review comment.")
	})

	t.Run("warns when author who is not a reviewer sets --reviewer-state", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient, &gitlab.BasicUser{ID: mr1AuthorID}, 7)
		mockDraftList(t, testClient, 1)
		expectPublishReviewed(t, testClient)
		testClient.MockUsers.EXPECT().
			CurrentUser(gomock.Any()).
			Return(&gitlab.User{ID: mr1AuthorID}, nil, nil)

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y --reviewer-state reviewed`)
		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "GitLab did not set your review state: you authored this merge request and are not one of its reviewers.")
		assert.Contains(t, output.String(), "✓ Published 1 pending review comment.")
	})

	t.Run("no warning when author is also a reviewer", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient, &gitlab.BasicUser{ID: mr1AuthorID}, 7, mr1AuthorID)
		mockDraftList(t, testClient, 1)
		expectPublishReviewed(t, testClient)
		testClient.MockUsers.EXPECT().
			CurrentUser(gomock.Any()).
			Return(&gitlab.User{ID: mr1AuthorID}, nil, nil)

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y --reviewer-state reviewed`)
		require.NoError(t, err)
		assert.NotContains(t, output.Stderr(), "GitLab did not set your review state")
		assert.Contains(t, output.String(), "✓ Published 1 pending review comment.")
	})

	t.Run("no warning when a different user sets --reviewer-state", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient, &gitlab.BasicUser{ID: mr1AuthorID})
		mockDraftList(t, testClient, 1)
		expectPublishReviewed(t, testClient)
		testClient.MockUsers.EXPECT().
			CurrentUser(gomock.Any()).
			Return(&gitlab.User{ID: 7}, nil, nil)

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y --reviewer-state reviewed`)
		require.NoError(t, err)
		assert.NotContains(t, output.Stderr(), "GitLab did not set your review state")
		assert.Contains(t, output.String(), "✓ Published 1 pending review comment.")
	})

	t.Run("publishes without warning when the current user lookup fails", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient, &gitlab.BasicUser{ID: mr1AuthorID})
		mockDraftList(t, testClient, 1)
		expectPublishReviewed(t, testClient)
		testClient.MockUsers.EXPECT().
			CurrentUser(gomock.Any()).
			Return(nil, nil, errors.New("boom"))

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y --reviewer-state reviewed`)
		require.NoError(t, err)
		assert.NotContains(t, output.Stderr(), "GitLab did not set your review state")
		assert.Contains(t, output.String(), "✓ Published 1 pending review comment.")
	})

	t.Run("no author check when publish fails", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient, &gitlab.BasicUser{ID: mr1AuthorID})
		mockDraftList(t, testClient, 1)
		testClient.MockDraftNotes.EXPECT().
			PublishAllDraftNotesWithOptions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("boom"))

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y --reviewer-state reviewed`)
		require.ErrorContains(t, err, "failed to publish pending review comments")
		assert.NotContains(t, output.Stderr(), "GitLab did not set your review state")
	})

	t.Run("errors when there are no pending comments", func(t *testing.T) {
		t.Parallel()

		testClient := setupMR(t)
		mockDraftList(t, testClient, 0)

		exec := setupPublishExec(t, testClient)

		_, err := exec(`1 -y`)
		require.ErrorContains(t, err, "no pending review comments on !1")
	})

	t.Run("--internal requires --message", func(t *testing.T) {
		t.Parallel()

		exec := setupPublishExec(t, gitlabtesting.NewTestClient(t))

		_, err := exec(`1 -y --internal`)
		require.ErrorContains(t, err, "--internal requires --message")
	})

	t.Run("rejects whitespace-only --message", func(t *testing.T) {
		t.Parallel()

		exec := setupPublishExec(t, gitlabtesting.NewTestClient(t))

		_, err := exec(`1 -y -m "   "`)
		require.ErrorContains(t, err, "--message cannot be empty")
	})

	t.Run("rejects unknown reviewer state", func(t *testing.T) {
		t.Parallel()

		exec := setupPublishExec(t, gitlabtesting.NewTestClient(t))

		_, err := exec(`1 -y --reviewer-state bogus`)
		require.ErrorContains(t, err, "must be one of")
	})

	t.Run("--yes required non-interactively, before any API call", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`1`)
		require.ErrorContains(t, err, "--yes required when not running interactively")
	})

	t.Run("ListDraftNotes error wrapped", func(t *testing.T) {
		t.Parallel()

		testClient := setupMR(t)

		testClient.MockDraftNotes.EXPECT().
			ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(nil, nil, errors.New("boom"))

		exec := setupPublishExec(t, testClient)

		_, err := exec(`1 -y`)
		require.ErrorContains(t, err, "failed to list pending review comments: boom")
	})

	t.Run("paginates draft list", func(t *testing.T) {
		t.Parallel()

		testClient := setupMR(t)

		page1 := make([]*gitlab.DraftNote, 100)
		for i := range page1 {
			page1[i] = &gitlab.DraftNote{ID: int64(701 + i)}
		}
		page2 := []*gitlab.DraftNote{{ID: 801}, {ID: 802}, {ID: 803}}

		gomock.InOrder(
			testClient.MockDraftNotes.EXPECT().
				ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
				Return(page1, &gitlab.Response{NextPage: 2}, nil),
			testClient.MockDraftNotes.EXPECT().
				ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
				Return(page2, &gitlab.Response{NextPage: 0}, nil),
		)

		testClient.MockDraftNotes.EXPECT().
			PublishAllDraftNotesWithOptions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(nil, nil)

		exec := setupPublishExec(t, testClient)

		output, err := exec(`1 -y`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Published 103 pending review comments.")
	})

	t.Run("PublishAllDraftNotes error wrapped", func(t *testing.T) {
		t.Parallel()

		testClient := setupMR(t)
		mockDraftList(t, testClient, 1)

		testClient.MockDraftNotes.EXPECT().
			PublishAllDraftNotesWithOptions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("boom"))

		exec := setupPublishExec(t, testClient)

		_, err := exec(`1 -y`)
		require.ErrorContains(t, err, "failed to publish pending review comments: boom")
	})
}

func TestDraftPublish_DeclinePrompt(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).
	testClient := setupMR(t)
	mockDraftList(t, testClient, 2)
	testClient.MockDraftNotes.EXPECT().
		PublishAllDraftNotesWithOptions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	c := ugh.New(t)
	c.Expect(ugh.Confirm("Publish 2 pending review comments?")).
		Do(ugh.Reject)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithConsole(t, c),
	)

	out, err := exec(`1`)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Aborted.")
}

func setupMR(t *testing.T) *gitlabtesting.TestClient {
	t.Helper()
	testClient := gitlabtesting.NewTestClient(t)
	mockMR1(t, testClient, nil)
	return testClient
}

func mockMR1(t *testing.T, tc *gitlabtesting.TestClient, author *gitlab.BasicUser, reviewerIDs ...int64) {
	t.Helper()
	reviewers := make([]*gitlab.BasicUser, 0, len(reviewerIDs))
	for _, id := range reviewerIDs {
		reviewers = append(reviewers, &gitlab.BasicUser{ID: id})
	}
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:        1,
				IID:       1,
				WebURL:    "https://gitlab.com/OWNER/REPO/merge_requests/1",
				Author:    author,
				Reviewers: reviewers,
			},
		}, nil, nil)
}

func mockDraftList(t *testing.T, testClient *gitlabtesting.TestClient, count int) {
	t.Helper()
	drafts := make([]*gitlab.DraftNote, count)
	for i := range drafts {
		drafts[i] = &gitlab.DraftNote{ID: int64(701 + i)}
	}
	testClient.MockDraftNotes.EXPECT().
		ListDraftNotes("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		Return(drafts, &gitlab.Response{NextPage: 0}, nil)
}

func expectPublishReviewed(t *testing.T, testClient *gitlabtesting.TestClient) {
	t.Helper()
	testClient.MockDraftNotes.EXPECT().
		PublishAllDraftNotesWithOptions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		DoAndReturn(func(pid any, mrIID int64, opts *gitlab.PublishAllDraftNotesOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
			require.NotNil(t, opts.ReviewerState)
			assert.Equal(t, "reviewed", *opts.ReviewerState)
			return nil, nil
		})
}

func setupPublishExec(t *testing.T, testClient *gitlabtesting.TestClient) cmdtest.CmdExecFunc {
	t.Helper()
	return cmdtest.SetupCmdForTest(t, NewCmd, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)
}
