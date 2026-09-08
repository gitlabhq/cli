//go:build !integration

package view

import (
	"errors"
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

func Test_RepositoryView(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		GetSingleRegistryRepository(int64(101), gomock.Any()).
		DoAndReturn(func(pid any, opt *gitlab.GetSingleRegistryRepositoryOptions, options ...gitlab.RequestOptionFunc) (*gitlab.RegistryRepository, *gitlab.Response, error) {
			assert.True(t, *opt.Tags)
			assert.True(t, *opt.TagsCount)

			return &gitlab.RegistryRepository{
				ID:        101,
				Name:      "app",
				Path:      "OWNER/REPO/app",
				ProjectID: 7,
				Location:  "registry.gitlab.com/owner/repo/app",
				TagsCount: 3,
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --include-tags --include-tags-count")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "OWNER/REPO/app")
	assert.Contains(t, out.String(), "registry.gitlab.com/owner/repo/app")
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryView_DoesNotRequireBaseRepo(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		GetSingleRegistryRepository(int64(101), gomock.Any()).
		Return(&gitlab.RegistryRepository{ID: 101, Path: "OWNER/REPO/app"}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepoError(errors.New("base repo should not be required")),
	)

	out, err := exec("101")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "OWNER/REPO/app")
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryView_CanSkipTagsCount(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		GetSingleRegistryRepository(int64(101), gomock.Any()).
		DoAndReturn(func(pid any, opt *gitlab.GetSingleRegistryRepositoryOptions, options ...gitlab.RequestOptionFunc) (*gitlab.RegistryRepository, *gitlab.Response, error) {
			assert.Nil(t, opt.TagsCount)

			return &gitlab.RegistryRepository{ID: 101, Path: "OWNER/REPO/app"}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --include-tags-count=false")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "OWNER/REPO/app")
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryView_JSONUsesListSchema(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		GetSingleRegistryRepository(int64(101), gomock.Any()).
		Return(&gitlab.RegistryRepository{
			ID:        101,
			Name:      "app",
			Path:      "OWNER/REPO/app",
			ProjectID: 7,
			Location:  "registry.gitlab.com/owner/repo/app",
			TagsCount: 3,
			Tags: []*gitlab.RegistryRepositoryTag{
				{
					Name:     "latest",
					Path:     "OWNER/REPO/app:latest",
					Location: "registry.gitlab.com/owner/repo/app:latest",
				},
			},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --include-tags --output json")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"tags_count":3`)
	assert.Contains(t, out.String(), `"tags":[{"name":"latest"`)
	assert.NotContains(t, out.String(), `"revision":""`)
	assert.NotContains(t, out.String(), `"total_size":0`)
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryView_InvalidID(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("abc")
	require.Error(t, err)
	assert.Equal(t, "repository ID must be a positive integer", err.Error())
}

func Test_RepositoryView_APIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		GetSingleRegistryRepository(int64(101), gomock.Any()).
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
	assert.Equal(t, "failed to fetch container registry repository 101.", exitErr.Details)
}
