//go:build !integration

package revoke

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRevoke(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerTokens.EXPECT().
		RevokeRunnerControllerToken(int64(42), int64(1), gomock.Any()).
		Return(nil, nil)

	out, err := exec("42 1 --force")
	require.NoError(t, err)
	assert.Equal(t, "Revoked token 1 from runner controller 42\n", out.OutBuf.String())
}

func TestRevokeError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerTokens.EXPECT().
		RevokeRunnerControllerToken(int64(42), int64(1), gomock.Any()).
		Return(nil, errors.New("API error"))

	_, err := exec("42 1 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestRevokeInvalidControllerID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("invalid 1 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestRevokeInvalidTokenID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42 invalid --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestRevokeRequiresForceNonInteractive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force required")
}
