//go:build !integration

package assign

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRunnerAssign_Success(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
	)

	tc.MockRunners.EXPECT().
		EnableProjectRunner("OWNER/REPO", &gitlab.EnableProjectRunnerOptions{RunnerID: 9}, gomock.Any()).
		Return(&gitlab.Runner{ID: 9, Description: "test-runner"}, nil, nil)

	// Default test factory uses OWNER/REPO as base repo
	out, err := exec("9")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Runner 9 has been assigned to project OWNER/REPO")
}

func TestRunnerAssign_WithRepoFlag(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
	)

	tc.MockRunners.EXPECT().
		EnableProjectRunner("group/subgroup/repo", &gitlab.EnableProjectRunnerOptions{RunnerID: 9}, gomock.Any()).
		Return(&gitlab.Runner{ID: 9}, nil, nil)

	out, err := exec("9 -R group/subgroup/repo")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Runner 9 has been assigned to project group/subgroup/repo")
}

func TestRunnerAssign_InvalidRunnerID(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	// Don't use -R so we don't depend on parent command; error is from invalid runner ID
	_, err := exec("invalid")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestRunnerAssign_RequiresRepo(t *testing.T) {
	t.Parallel()

	// No base repo: must use -R to specify the project
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithBaseRepoError(errors.New("not in a git repo")))
	_, err := exec("9")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "-R is required")
}
