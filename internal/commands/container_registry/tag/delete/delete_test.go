//go:build !integration

package delete

import (
	"fmt"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_TagDelete(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepositoryTag("OWNER/REPO", int64(101), "latest").
		Return(nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 latest --yes")
	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`• Deleting container registry tag OWNER/REPO:latest
		✓ Container registry tag "latest" deleted.
	`), out.String())
	assert.Empty(t, out.Stderr())
}

func Test_TagDelete_RequiresYesWhenNotInteractive(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("101 latest")
	require.Error(t, err)
	assert.Equal(t, "--yes or -y flag is required when not running interactively", err.Error())
}

func Test_TagDelete_APIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepositoryTag("OWNER/REPO", int64(101), "latest").
		Return(nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101 latest --yes")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, `failed to delete container registry tag "latest" from repository 101 on OWNER/REPO; ensure the container registry repository belongs to OWNER/REPO, or specify the owning project with -R <project>.`, exitErr.Details)
}

func Test_TagDelete_Bulk(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		DoAndReturn(func(pid any, repository int64, opt *gitlab.DeleteRegistryRepositoryTagsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
			assert.Equal(t, "^release-.*", *opt.NameRegexpDelete)
			assert.Equal(t, "^latest$", *opt.NameRegexpKeep)
			assert.Equal(t, int64(5), *opt.KeepN)
			assert.Equal(t, "30d", *opt.OlderThan)

			return nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --name-regex-delete '^release-.*' --name-regex-keep '^latest$' --keep-n 5 --older-than 30d --yes")
	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`• Scheduling container registry tags for deletion repo=OWNER/REPO repository=101
		✓ Container registry tags scheduled for deletion. They may remain visible until GitLab finishes the background deletion job.
	`), out.String())
	assert.Empty(t, out.Stderr())
}

func Test_TagDelete_BulkAcceptsAnyBulkFlag(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		DoAndReturn(func(pid any, repository int64, opt *gitlab.DeleteRegistryRepositoryTagsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
			assert.Nil(t, opt.NameRegexpDelete)
			assert.Nil(t, opt.NameRegexpKeep)
			assert.Nil(t, opt.KeepN)
			assert.Equal(t, "30d", *opt.OlderThan)

			return nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101 --older-than 30d --yes")
	require.NoError(t, err)
}

func Test_TagDelete_BulkSendsExplicitKeepNZero(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		DoAndReturn(func(pid any, repository int64, opt *gitlab.DeleteRegistryRepositoryTagsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
			require.NotNil(t, opt.KeepN)
			assert.Equal(t, int64(0), *opt.KeepN)

			return nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101 --keep-n 0 --yes")
	require.NoError(t, err)
}

func Test_TagDelete_RequiresTagNameOrBulkFlag(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("101 --yes")
	require.Error(t, err)
	assert.Equal(t, "either a tag name or at least one bulk deletion flag is required", err.Error())
}

func Test_TagDelete_RejectsTagNameWithBulkFlags(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("101 latest --older-than 30d --yes")
	require.Error(t, err)
	assert.Equal(t, "either a tag name or bulk deletion flags must be passed, but not both", err.Error())
}

func Test_TagDelete_RejectsInvalidNameRegexDelete(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("101 --name-regex-delete '[' --yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name-regex-delete is not a valid regular expression")
}

func Test_TagDelete_RejectsInvalidNameRegexKeep(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("101 --name-regex-keep '[' --yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name-regex-keep is not a valid regular expression")
}

func Test_TagDelete_RejectsNegativeKeepN(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("101 --keep-n -1 --yes")
	require.Error(t, err)
	assert.Equal(t, "--keep-n must be zero or a positive integer", err.Error())
}

func Test_TagDelete_BulkRequiresConfirmationWithFilters(t *testing.T) {
	t.Parallel()

	got := bulkDeleteConfirmationMessage(101, "OWNER/REPO", "^release-.*", "^latest$", 5, "30d")

	assert.Contains(t, got, "This action schedules container registry tags for deletion from repository 101 on OWNER/REPO.")
	assert.Contains(t, got, "name regex delete: ^release-.*")
	assert.Contains(t, got, "name regex keep: ^latest$")
	assert.Contains(t, got, "keep latest: 5")
	assert.Contains(t, got, "older than: 30d")
	assert.Contains(t, got, "The matching tags may remain visible until the background deletion job has completed.")
}

func Test_TagDelete_BulkAPIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		Return(nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101 --name-regex-delete '.*' --yes")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to delete container registry tags from repository 101 on OWNER/REPO; ensure the container registry repository belongs to OWNER/REPO, or specify the owning project with -R <project>.", exitErr.Details)
}
