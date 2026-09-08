//go:build !integration

package note

import (
	"fmt"
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

func makeDiscussionsWithAuthor() []*gitlab.Discussion {
	return []*gitlab.Discussion{
		{
			ID: "abc12345deadbeef1234567890abcdef12345678",
			Notes: []*gitlab.Note{
				{
					ID:   100,
					Body: "First discussion note",
					Author: gitlab.NoteAuthor{
						Username: "testuser",
					},
				},
			},
		},
		{
			ID: "def67890cafebabe1234567890abcdef12345678",
			Notes: []*gitlab.Note{
				{
					ID:   200,
					Body: "Second discussion note",
					Author: gitlab.NoteAuthor{
						Username: "otheruser",
					},
				},
			},
		},
	}
}

func Test_delete_subcommand(t *testing.T) {
	t.Parallel()

	t.Run("delete with --yes flag", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussionsWithAuthor(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			DeleteMergeRequestDiscussionNote(
				"OWNER/REPO",
				int64(1),
				"abc12345deadbeef1234567890abcdef12345678",
				int64(100),
				gomock.Any(),
			).
			Return(nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`delete 1 100 --yes`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Deleted note 100 from !1")
	})

	t.Run("delete non-TTY skips confirmation", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussionsWithAuthor(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			DeleteMergeRequestDiscussionNote(
				"OWNER/REPO",
				int64(1),
				"def67890cafebabe1234567890abcdef12345678",
				int64(200),
				gomock.Any(),
			).
			Return(nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`delete 1 200 --yes`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "✓ Deleted note 200 from !1")
	})

	t.Run("non-TTY without --yes requires flag", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussionsWithAuthor(), nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`delete 1 100`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--yes required when not running interactively")
	})

	t.Run("non-integer identifier", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`delete 1 abc12345`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid note ID")
		assert.Contains(t, err.Error(), "must be a numeric note ID")
	})

	t.Run("note ID not found", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussionsWithAuthor(), nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`delete 1 999999`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "note 999999 not found in merge request !1")
	})

	t.Run("API error", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussionsWithAuthor(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			DeleteMergeRequestDiscussionNote(
				"OWNER/REPO",
				int64(1),
				"abc12345deadbeef1234567890abcdef12345678",
				int64(100),
				gomock.Any(),
			).
			Return(nil, fmt.Errorf("403 Forbidden"))

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`delete 1 100 --yes`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete note")
	})
}
