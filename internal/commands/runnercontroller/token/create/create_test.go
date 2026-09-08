//go:build !integration

package create

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

func TestCreate(t *testing.T) {
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
		CreateRunnerControllerToken(int64(42), gomock.Any(), gomock.Any()).
		Return(&gitlab.RunnerControllerToken{
			ID:                 1,
			RunnerControllerID: 42,
			Description:        "production",
			Token:              "any-token",
			CreatedAt:          &fixedTime,
			UpdatedAt:          &fixedTime,
		}, nil, nil)

	out, err := exec("42 --description 'production'")
	require.NoError(t, err)
	output := out.OutBuf.String()
	assert.Contains(t, output, "Created token 1 for runner controller 42")
	assert.Contains(t, output, "Token: any-token")
	assert.Contains(t, output, "Warning: Save this token")
}

func TestCreateJSON(t *testing.T) {
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
		CreateRunnerControllerToken(int64(42), gomock.Any(), gomock.Any()).
		Return(&gitlab.RunnerControllerToken{
			ID:                 1,
			RunnerControllerID: 42,
			Description:        "production",
			Token:              "any-token",
			CreatedAt:          &fixedTime,
			UpdatedAt:          &fixedTime,
		}, nil, nil)

	out, err := exec("42 --output json")
	require.NoError(t, err)
	output := out.OutBuf.String()
	assert.Contains(t, output, `"id":1`)
	assert.Contains(t, output, `"token":"any-token"`)
}

func TestCreateError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerTokens.EXPECT().
		CreateRunnerControllerToken(int64(42), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("API error"))

	_, err := exec("42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestCreateInvalidID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}
