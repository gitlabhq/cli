//go:build !integration

package view

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_TagView(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		GetRegistryRepositoryTagDetail("OWNER/REPO", int64(101), "latest").
		Return(&gitlab.RegistryRepositoryTag{
			Name:          "latest",
			Path:          "OWNER/REPO/app:latest",
			Location:      "registry.gitlab.com/owner/repo/app:latest",
			Revision:      "abcdef",
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

	out, err := exec("101 latest")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "latest")
	assert.Contains(t, out.String(), "sha256:abc")
	assert.Empty(t, out.Stderr())
}

func Test_TagView_JSON(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
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

	out, err := exec("101 latest --output json")
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"name":"latest"`)
	assert.Contains(t, out.String(), `"digest":"sha256:abc"`)
	assert.Contains(t, out.String(), `"total_size":1024`)
	assert.Empty(t, out.Stderr())
}

func Test_TagView_APIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		GetRegistryRepositoryTagDetail("OWNER/REPO", int64(101), "latest").
		Return(nil, nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101 latest")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, `failed to fetch container registry tag details for tag "latest" from repository 101 on OWNER/REPO; ensure the container registry repository belongs to OWNER/REPO, or specify the owning project with -R <project>.`, exitErr.Details)
}
