//go:build !integration

package delete

import (
	"fmt"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_RepositoryDelete(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepository("OWNER/REPO", int64(101)).
		Return(nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("101 --yes")
	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`• Deleting container registry repository repo=OWNER/REPO repository=101
		✓ Container registry repository 101 deleted.
	`), out.String())
	assert.Empty(t, out.Stderr())
}

func Test_RepositoryDelete_RequiresYesWhenNotInteractive(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("101")
	require.Error(t, err)
	assert.Equal(t, "--yes or -y flag is required when not running interactively", err.Error())
}

func Test_RepositoryDelete_APIError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockContainerRegistry.EXPECT().
		DeleteRegistryRepository("OWNER/REPO", int64(101)).
		Return(nil, fmt.Errorf("api failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	_, err := exec("101 --yes")
	require.Error(t, err)
	assert.Equal(t, "api failed", err.Error())

	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "failed to delete container registry repository 101 on OWNER/REPO; ensure the container registry repository belongs to OWNER/REPO, or specify the owning project with -R <project>.", exitErr.Details)
}
