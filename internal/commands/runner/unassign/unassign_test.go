//go:build !integration

package unassign

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRunnerUnassign_Success(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
	)

	tc.MockRunners.EXPECT().
		DisableProjectRunner("OWNER/REPO", int64(9), gomock.Any()).
		Return(nil, nil)

	out, err := exec("9")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Runner 9 has been unassigned from project OWNER/REPO")
}

func TestRunnerUnassign_WithRepoFlag(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
	)

	tc.MockRunners.EXPECT().
		DisableProjectRunner("group/subgroup/repo", int64(9), gomock.Any()).
		Return(nil, nil)

	out, err := exec("9 -R group/subgroup/repo")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Runner 9 has been unassigned from project group/subgroup/repo")
}

func TestRunnerUnassign_InvalidRunnerID(t *testing.T) {
	t.Parallel()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestRunnerUnassign_RequiresRepo(t *testing.T) {
	t.Parallel()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithBaseRepoError(errors.New("not in a git repo")))
	_, err := exec("9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "-R is required")
}

func TestRunnerUnassign_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
	)

	tc.MockRunners.EXPECT().
		DisableProjectRunner("OWNER/REPO", int64(9), gomock.Any()).
		Return(nil, errors.New("runner is locked"))

	_, err := exec("9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runner is locked")
}
