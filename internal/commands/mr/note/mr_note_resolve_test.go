//go:build !integration

package note

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func makeDiscussions() []*gitlab.Discussion {
	return []*gitlab.Discussion{
		{
			ID: "abc12345deadbeef1234567890abcdef12345678",
			Notes: []*gitlab.Note{
				{ID: 100, Body: "First discussion"},
			},
		},
		{
			ID: "def67890cafebabe1234567890abcdef12345678",
			Notes: []*gitlab.Note{
				{ID: 200, Body: "Second discussion"},
			},
		},
	}
}

// makeMRMock is an alias for mockMR1 kept for readability in this file.
var makeMRMock = mockMR1

func Test_resolve_subcommand(t *testing.T) {
	t.Parallel()

	t.Run("resolve by prefix", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), "abc12345deadbeef1234567890abcdef12345678", gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: "abc12345deadbeef1234567890abcdef12345678"}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`resolve 1 abc12345`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion resolved (abc12345… in !1)")
	})

	t.Run("resolve by full ID", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		fullID := "abc12345deadbeef1234567890abcdef12345678"

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), fullID, gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: fullID}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`resolve 1 ` + fullID)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion resolved")
	})

	t.Run("resolve by note ID", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), "abc12345deadbeef1234567890abcdef12345678", gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: "abc12345deadbeef1234567890abcdef12345678"}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`resolve 1 100`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion resolved")
	})

	t.Run("resolve by note ID found on second page", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		nextPageResponse := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusOK},
			NextPage: 2,
		}
		lastPageResponse := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusOK},
		}

		// First page: no match.
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID: "abc12345deadbeef1234567890abcdef12345678",
					Notes: []*gitlab.Note{
						{ID: 100, Body: "First discussion"},
					},
				},
			}, nextPageResponse, nil)

		// Second page: match. No further pages should be requested.
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID: "def67890cafebabe1234567890abcdef12345678",
					Notes: []*gitlab.Note{
						{ID: 200, Body: "Second discussion"},
					},
				},
			}, lastPageResponse, nil)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), "def67890cafebabe1234567890abcdef12345678", gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: "def67890cafebabe1234567890abcdef12345678"}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`resolve 1 200`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion resolved")
	})

	t.Run("note ID not found", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 999999`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "note 999999 not found in merge request !1")
	})

	t.Run("note ID not found across multiple pages", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		nextPageResponse := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusOK},
			NextPage: 2,
		}
		lastPageResponse := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusOK},
		}

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nextPageResponse, nil)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID: "aaaa1111bbbb2222cccc3333dddd4444eeee5555",
					Notes: []*gitlab.Note{
						{ID: 300, Body: "Third discussion"},
					},
				},
			}, lastPageResponse, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 999999`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "note 999999 not found in merge request !1")
	})

	t.Run("underlying API error for note ID lookup is preserved", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		forbiddenResponse := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusForbidden},
		}
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(nil, forbiddenResponse, fmt.Errorf("403 Forbidden"))

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 999999`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403 Forbidden")
		assert.NotContains(t, err.Error(), "not found", "a real API failure must not be reported as note-not-found")
	})

	t.Run("prefix too short", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 abc`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least 8 characters")
	})

	t.Run("discussion not found", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 zzz12345`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no discussion found")
	})

	t.Run("ambiguous prefix", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		ambiguous := []*gitlab.Discussion{
			{
				ID:    "abc12345aaaa0000000000000000000000000001",
				Notes: []*gitlab.Note{{ID: 100}},
			},
			{
				ID:    "abc12345bbbb0000000000000000000000000002",
				Notes: []*gitlab.Note{{ID: 200}},
			},
		}
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(ambiguous, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 abc12345`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matches 2 discussions: abc12345")
	})
}

func Test_reopen_subcommand(t *testing.T) {
	t.Parallel()

	t.Run("reopen by prefix", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), "def67890cafebabe1234567890abcdef12345678", gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: "def67890cafebabe1234567890abcdef12345678"}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`reopen 1 def67890`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion reopened (def67890… in !1)")
	})

	t.Run("reopen API error", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), "abc12345deadbeef1234567890abcdef12345678", gomock.Any(), gomock.Any()).
			Return(nil, nil, fmt.Errorf("403 Forbidden"))

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`reopen 1 abc12345`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to reopen discussion")
	})

	t.Run("too many args", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`reopen 1 abc12345 extra`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "accepts between 1 and 2 arg(s)")
	})

	t.Run("no args", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(t, NewCmdNote, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`reopen`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "accepts between 1 and 2 arg(s)")
	})
}
