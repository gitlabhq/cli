//go:build !integration

package list

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_TagList(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		DoAndReturn(func(pid any, repository int64, opt *gitlab.ListRegistryRepositoryTagsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.RegistryRepositoryTag, *gitlab.Response, error) {
			assert.Equal(t, int64(2), opt.Page)
			assert.Equal(t, int64(50), opt.PerPage)

			return []*gitlab.RegistryRepositoryTag{
				{
					Name:     "latest",
					Path:     "OWNER/REPO/app:latest",
					Location: "registry.gitlab.example.com/OWNER/REPO/app:latest",
				},
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --page 2 --per-page 50")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Showing 1 container registry tag on OWNER/REPO. (Page 2)")
	assert.Contains(t, out.String(), "latest")
	assert.Contains(t, out.String(), "Location")
	assert.NotContains(t, out.String(), "0 B")
	assert.NotContains(t, out.String(), "Digest")
	assert.Empty(t, out.Stderr())
}

func Test_TagList_WithDetails(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		Return([]*gitlab.RegistryRepositoryTag{{Name: "latest"}}, nil, nil)
	testClient.MockContainerRegistry.EXPECT().
		GetRegistryRepositoryTagDetail("OWNER/REPO", int64(101), "latest").
		Return(&gitlab.RegistryRepositoryTag{
			Name:      "latest",
			Path:      "OWNER/REPO/app:latest",
			Digest:    "sha256:abc",
			TotalSize: 1024,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --details")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Digest")
	assert.Contains(t, out.String(), "sha256:abc")
	assert.Contains(t, out.String(), "1.0 kB")
	assert.Empty(t, out.Stderr())
}

func Test_TagList_WithDetailsAPIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		Return([]*gitlab.RegistryRepositoryTag{{Name: "latest"}}, nil, nil)
	testClient.MockContainerRegistry.EXPECT().
		GetRegistryRepositoryTagDetail("OWNER/REPO", int64(101), "latest").
		Return(nil, nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101 --details")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, `failed to fetch container registry tag details for tag "latest" from repository 101 on OWNER/REPO; ensure the container registry repository belongs to OWNER/REPO, or specify the owning project with -R <project>.`, exitErr.Details)
}

func Test_TagList_JSON(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		Return([]*gitlab.RegistryRepositoryTag{{Name: "latest"}}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --output json")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"name":"latest"`)
	assert.NotContains(t, out.String(), `"revision":""`)
	assert.NotContains(t, out.String(), `"total_size":0`)
	assert.Empty(t, out.Stderr())
}

func Test_TagList_JSONWithDetails(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		Return([]*gitlab.RegistryRepositoryTag{{Name: "latest"}}, nil, nil)
	testClient.MockContainerRegistry.EXPECT().
		GetRegistryRepositoryTagDetail("OWNER/REPO", int64(101), "latest").
		Return(&gitlab.RegistryRepositoryTag{
			Name:      "latest",
			Path:      "OWNER/REPO/app:latest",
			Digest:    "sha256:abc",
			TotalSize: 1024,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --details --output json")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"digest":"sha256:abc"`)
	assert.Contains(t, out.String(), `"total_size":1024`)
	assert.Empty(t, out.Stderr())
}

func Test_TagList_EmptyResults(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		Return([]*gitlab.RegistryRepositoryTag{}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "No container registry tags available on OWNER/REPO.")
	assert.NotContains(t, out.String(), "Name\tPath\tDigest")
	assert.Empty(t, out.Stderr())
}

func Test_TagList_APIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListRegistryRepositoryTags("OWNER/REPO", int64(101), gomock.Any()).
		Return(nil, nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to fetch container registry tags from repository 101 on OWNER/REPO; ensure the container registry repository belongs to OWNER/REPO, or specify the owning project with -R <project>.", exitErr.Details)
}
