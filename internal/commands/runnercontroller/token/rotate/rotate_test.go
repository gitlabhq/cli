//go:build !integration

package rotate

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRotate(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	tc.MockRunnerControllerTokens.EXPECT().
		RotateRunnerControllerToken(int64(42), int64(1), gomock.Any()).
		Return(&gitlab.RunnerControllerToken{
			ID:                 1,
			RunnerControllerID: 42,
			Description:        "production",
			Token:              "any-new-token",
			CreatedAt:          &fixedTime,
			UpdatedAt:          &fixedTime,
		}, nil, nil)

	out, err := exec("42 1 --force")
	require.NoError(t, err)
	output := out.OutBuf.String()
	assert.Contains(t, output, "Rotated token 1 for runner controller 42")
	assert.Contains(t, output, "Token: any-new-token")
	assert.Contains(t, output, "Warning: Save this token")
}

func TestRotateJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	tc.MockRunnerControllerTokens.EXPECT().
		RotateRunnerControllerToken(int64(42), int64(1), gomock.Any()).
		Return(&gitlab.RunnerControllerToken{
			ID:                 1,
			RunnerControllerID: 42,
			Description:        "production",
			Token:              "any-new-token",
			CreatedAt:          &fixedTime,
			UpdatedAt:          &fixedTime,
		}, nil, nil)

	out, err := exec("42 1 --force --output json")
	require.NoError(t, err)
	output := out.OutBuf.String()
	assert.Contains(t, output, `"id":1`)
	assert.Contains(t, output, `"token":"any-new-token"`)
}

func TestRotateError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerTokens.EXPECT().
		RotateRunnerControllerToken(int64(42), int64(1), gomock.Any()).
		Return(nil, nil, errors.New("API error"))

	_, err := exec("42 1 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestRotateInvalidControllerID(t *testing.T) {
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

func TestRotateInvalidTokenID(t *testing.T) {
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

func TestRotateRequiresForceNonInteractive(t *testing.T) {
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
