//go:build !integration

package note

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 999999`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403 Forbidden")
		assert.NotContains(t, err.Error(), "not found", "a real API failure must not be reported as note-not-found")
	})

	t.Run("resolve by all-digit prefix when no note has that ID", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		digitID := "428736269e25094ae8737206d5af19a5f7ebc8ab"
		discussions := append(makeDiscussions(), &gitlab.Discussion{
			ID:    digitID,
			Notes: []*gitlab.Note{{ID: 300}},
		})
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(discussions, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil).
			Times(2)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), digitID, gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: digitID}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`resolve 1 42873626`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion resolved (42873626… in !1)")
	})

	t.Run("note ID wins over an all-digit discussion prefix", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		noteThreadID := "abc12345deadbeef1234567890abcdef12345678"
		discussions := []*gitlab.Discussion{
			{ID: noteThreadID, Notes: []*gitlab.Note{{ID: 42873626}}},
			{ID: "428736269e25094ae8737206d5af19a5f7ebc8ab", Notes: []*gitlab.Note{{ID: 300}}},
		}
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(discussions, nil, nil)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), noteThreadID, gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: noteThreadID}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`resolve 1 42873626`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion resolved (abc12345… in !1)")
	})

	t.Run("all-digit value matching neither a note nor a discussion", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil).
			Times(2)

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`resolve 1 12345678`)
		require.Error(t, err)
		assert.Equal(t, `no note 12345678 in merge request !1, and no discussion found matching prefix "12345678"`, err.Error())
	})

	t.Run("prefix too short", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`reopen 1 def67890`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion reopened (def67890… in !1)")
	})

	t.Run("reopen by all-digit prefix when no note has that ID", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		makeMRMock(t, testClient)

		digitID := "428736269e25094ae8737206d5af19a5f7ebc8ab"
		discussions := append(makeDiscussions(), &gitlab.Discussion{
			ID:    digitID,
			Notes: []*gitlab.Note{{ID: 300}},
		})
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(discussions, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil).
			Times(2)

		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), digitID, gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: digitID}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`reopen 1 42873626`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Discussion reopened (42873626… in !1)")
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
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

		exec := cmdtest.SetupCmdForTest(t, newNoteCmdWithRepo, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`reopen`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "accepts between 1 and 2 arg(s)")
	})
}

func Test_resolve_reopen_repoOverride(t *testing.T) {
	t.Parallel()

	const fullID = "abc12345deadbeef1234567890abcdef12345678"

	commands := []struct {
		name    string
		newCmd  func(cmdutils.Factory) *cobra.Command
		resolve bool
		past    string
	}{
		{"resolve", NewCmdResolve, true, "resolved"},
		{"reopen", NewCmdReopen, false, "reopened"},
	}

	for _, tc := range commands {
		setup := func(t *testing.T, testClient *gitlabtesting.TestClient) cmdtest.CmdExecFunc {
			t.Helper()
			return cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				cmd := tc.newCmd(f)
				cmdutils.EnableRepoOverride(cmd, f)
				return cmd
			}, true,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("WRONG", "REPO", ""),
			)
		}

		t.Run(tc.name+" with MR first and -R", func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			mockMR1InRepo(t, testClient, "gitlab-org/cli")
			testClient.MockDiscussions.EXPECT().
				ListMergeRequestDiscussions("gitlab-org/cli", int64(1), gomock.Any(), gomock.Any()).
				Return(makeDiscussions(), nil, nil)
			testClient.MockDiscussions.EXPECT().
				ResolveMergeRequestDiscussion("gitlab-org/cli", int64(1), fullID, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, _ int64, _ string, opts *gitlab.ResolveMergeRequestDiscussionOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Discussion, *gitlab.Response, error) {
					assert.Equal(t, tc.resolve, *opts.Resolved)
					return &gitlab.Discussion{ID: fullID}, nil, nil
				})

			output, err := setup(t, testClient)(`1 abc12345 -R gitlab-org/cli`)
			require.NoError(t, err)
			assert.Contains(t, output.String(), "✓ Discussion "+tc.past)
		})

		t.Run(tc.name+" with only a discussion ID and -R is rejected", func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)

			_, err := setup(t, testClient)(fullID + ` -R gitlab-org/cli`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "merge request ID or branch is required with --repo")
		})

		t.Run(tc.name+" with discussion ID first is not accepted as MR", func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			testClient.MockMergeRequests.EXPECT().
				ListProjectMergeRequests("gitlab-org/cli", gomock.Any(), gomock.Any()).
				Return(nil, nil, nil).Times(1)

			_, err := setup(t, testClient)(fullID + ` 1 -R gitlab-org/cli`)
			assert.ErrorContains(t, err, `no open merge request available for "`+fullID+`"`)
		})

		t.Run(tc.name+" help examples are well formed", func(t *testing.T) {
			t.Parallel()

			example := tc.newCmd(cmdtest.NewTestFactory(nil)).Example
			assert.NotContains(t, example, "%!")
			assert.NotContains(t, example, "glab mr note "+capitalize(tc.name))
			assert.Contains(t, example, "glab mr note "+tc.name+" 3107030349")
		})
	}
}

func newNoteCmdWithRepo(f cmdutils.Factory) *cobra.Command {
	cmd := NewCmdNote(f)
	cmdutils.EnableRepoOverride(cmd, f)
	return cmd
}
