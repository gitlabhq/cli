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

func Test_update_subcommand(t *testing.T) {
	t.Parallel()

	t.Run("update by note ID with message flag", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			UpdateMergeRequestDiscussionNote(
				"OWNER/REPO",
				int64(1),
				"abc12345deadbeef1234567890abcdef12345678",
				int64(100),
				gomock.Any(),
				gomock.Any(),
			).
			Return(&gitlab.Note{ID: 100}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`update 1 100 -m "Updated body"`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_100")
	})

	t.Run("update note in second discussion", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			UpdateMergeRequestDiscussionNote(
				"OWNER/REPO",
				int64(1),
				"def67890cafebabe1234567890abcdef12345678",
				int64(200),
				gomock.Any(),
				gomock.Any(),
			).
			Return(&gitlab.Note{ID: 200}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`update 1 200 -m "New body"`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "#note_200")
	})

	t.Run("message from stdin", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			UpdateMergeRequestDiscussionNote(
				"OWNER/REPO",
				int64(1),
				"abc12345deadbeef1234567890abcdef12345678",
				int64(100),
				gomock.Any(),
				gomock.Any(),
			).
			Return(&gitlab.Note{ID: 100}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithStdin("body from stdin"),
		)

		output, err := exec(`update 1 100`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "#note_100")
	})

	t.Run("empty message", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithStdin(""),
		)

		_, err := exec(`update 1 100`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "aborted: note has an empty message")
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

		_, err := exec(`update 1 abc12345 -m "body"`)
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
			Return(makeDiscussions(), nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`update 1 999999 -m "body"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "note 999999 not found in merge request !1")
	})

	t.Run("API error", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)
		mockMR1(t, testClient)

		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(makeDiscussions(), nil, nil)

		testClient.MockDiscussions.EXPECT().
			UpdateMergeRequestDiscussionNote(
				"OWNER/REPO",
				int64(1),
				"abc12345deadbeef1234567890abcdef12345678",
				int64(100),
				gomock.Any(),
				gomock.Any(),
			).
			Return(nil, nil, fmt.Errorf("403 Forbidden"))

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		_, err := exec(`update 1 100 -m "body"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update note")
	})
}
