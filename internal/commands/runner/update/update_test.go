//go:build !integration

package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRunnerUpdate_Pause(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	pausedTrue := true
	tc.MockRunners.EXPECT().
		UpdateRunnerDetails(int64(6), &gitlab.UpdateRunnerDetailsOptions{Paused: &pausedTrue}, gomock.Any()).
		Return(&gitlab.RunnerDetails{ID: 6, Paused: true}, nil, nil)

	out, err := exec("6 --pause")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Runner 6 has been paused")
	assert.Empty(t, out.Stderr())
}

func TestRunnerUpdate_Unpause(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	pausedFalse := false
	tc.MockRunners.EXPECT().
		UpdateRunnerDetails(int64(6), &gitlab.UpdateRunnerDetailsOptions{Paused: &pausedFalse}, gomock.Any()).
		Return(&gitlab.RunnerDetails{ID: 6, Paused: false}, nil, nil)

	out, err := exec("6 --unpause")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Runner 6 has been unpaused")
	assert.Empty(t, out.Stderr())
}

func TestRunnerUpdate_InvalidID(t *testing.T) {
	t.Parallel()
	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err := exec("not-a-number --pause")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid syntax")
}

func TestRunnerUpdate_NeitherPauseNorUnpause(t *testing.T) {
	t.Parallel()
	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err := exec("6")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags")
}
