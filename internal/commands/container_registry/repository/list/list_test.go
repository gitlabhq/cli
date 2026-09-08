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

func Test_RepositoryList_Project(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListProjectRegistryRepositories("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opt *gitlab.ListProjectRegistryRepositoriesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.RegistryRepository, *gitlab.Response, error) {
			assert.Equal(t, int64(2), opt.Page)
			assert.Equal(t, int64(50), opt.PerPage)
			assert.True(t, *opt.Tags)
			assert.True(t, *opt.TagsCount)

			return []*gitlab.RegistryRepository{
				{
					ID:        101,
					Name:      "app",
					Path:      "OWNER/REPO/app",
					TagsCount: 2,
				},
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--page 2 --per-page 50 --include-tags")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Showing 1 container registry repository on OWNER/REPO. (Page 2)")
	assert.Contains(t, out.String(), "OWNER/REPO/app")
	assert.NotContains(t, out.String(), "Status")
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryList_ProjectCanSkipTagsCount(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListProjectRegistryRepositories("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opt *gitlab.ListProjectRegistryRepositoriesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.RegistryRepository, *gitlab.Response, error) {
			assert.Nil(t, opt.TagsCount)

			return []*gitlab.RegistryRepository{{ID: 101, Path: "OWNER/REPO/app"}}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--include-tags-count=false")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "OWNER/REPO/app")
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryList_GroupJSON(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListGroupRegistryRepositories("gitlab-org", gomock.Any()).
		Return([]*gitlab.RegistryRepository{{ID: 102, Path: "gitlab-org/cli"}}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--group gitlab-org --output json")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"id":102`)
	assert.Contains(t, out.String(), `"path":"gitlab-org/cli"`)
	assert.NotContains(t, out.String(), `"tags":null`)
	assert.NotContains(t, out.String(), `"tags_count"`)
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryList_GroupOmitsTagsCount(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListGroupRegistryRepositories("gitlab-org", gomock.Any()).
		Return([]*gitlab.RegistryRepository{
			{
				ID:   102,
				Path: "gitlab-org/cli",
			},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--group gitlab-org")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "gitlab-org/cli")
	assert.NotContains(t, out.String(), "Tags")
	assert.NotContains(t, out.String(), "\t0\t")
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryList_JSONIncludesTagsWhenPresent(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListProjectRegistryRepositories("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opt *gitlab.ListProjectRegistryRepositoriesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.RegistryRepository, *gitlab.Response, error) {
			assert.True(t, *opt.Tags)

			return []*gitlab.RegistryRepository{
				{
					ID:   101,
					Path: "OWNER/REPO/app",
					Tags: []*gitlab.RegistryRepositoryTag{
						{
							Name:     "latest",
							Path:     "OWNER/REPO/app:latest",
							Location: "registry.gitlab.example.com/OWNER/REPO/app:latest",
						},
					},
				},
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--include-tags --output json")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"tags":[{"name":"latest"`)
	assert.Contains(t, out.String(), `"location":"registry.gitlab.example.com/OWNER/REPO/app:latest"`)
	assert.NotContains(t, out.String(), `"revision":""`)
	assert.NotContains(t, out.String(), `"short_revision":""`)
	assert.NotContains(t, out.String(), `"digest":""`)
	assert.NotContains(t, out.String(), `"total_size":0`)
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryList_JSONIncludesTagDetailsWhenRequested(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListProjectRegistryRepositories("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opt *gitlab.ListProjectRegistryRepositoriesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.RegistryRepository, *gitlab.Response, error) {
			assert.True(t, *opt.Tags)

			return []*gitlab.RegistryRepository{
				{
					ID:   101,
					Path: "OWNER/REPO/app",
					Tags: []*gitlab.RegistryRepositoryTag{
						{Name: "latest"},
					},
				},
			}, nil, nil
		})
	testClient.MockContainerRegistry.EXPECT().
		GetRegistryRepositoryTagDetail("OWNER/REPO", int64(101), "latest").
		Return(&gitlab.RegistryRepositoryTag{
			Name:          "latest",
			Path:          "OWNER/REPO/app:latest",
			Location:      "registry.gitlab.example.com/OWNER/REPO/app:latest",
			Revision:      "abc123",
			ShortRevision: "abc",
			Digest:        "sha256:abc",
			TotalSize:     1024,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("--include-tag-details --output json")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"revision":"abc123"`)
	assert.Contains(t, out.String(), `"short_revision":"abc"`)
	assert.Contains(t, out.String(), `"digest":"sha256:abc"`)
	assert.Contains(t, out.String(), `"total_size":1024`)
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryList_TagDetailsAPIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListProjectRegistryRepositories("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.RegistryRepository{
			{
				ID:   101,
				Tags: []*gitlab.RegistryRepositoryTag{{Name: "latest"}},
			},
		}, nil, nil)
	testClient.MockContainerRegistry.EXPECT().
		GetRegistryRepositoryTagDetail("OWNER/REPO", int64(101), "latest").
		Return(nil, nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("--include-tag-details --output json")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())
}

func Test_RepositoryList_TagDetailsRejectsGroup(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--group gitlab-org --include-tag-details --output json")
	require.Error(t, err)
	assert.Equal(t, "--include-tag-details is only available for project repositories", err.Error())
}

func Test_RepositoryList_TagDetailsRequiresJSONOutput(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--include-tag-details")
	require.Error(t, err)
	assert.Equal(t, "--include-tag-details requires --output json", err.Error())
}

func Test_RepositoryList_EmptyResults(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListProjectRegistryRepositories("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.RegistryRepository{}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "No container registry repositories available on OWNER/REPO.")
	assert.NotContains(t, out.String(), "ID\tName\tPath")
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryList_APIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		ListProjectRegistryRepositories("OWNER/REPO", gomock.Any()).
		Return(nil, nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())
	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to list container registry repositories from OWNER/REPO; ensure OWNER/REPO exists and has container registry enabled, or specify the owning project with -R <project>.", exitErr.Details)
}
