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
	tc.MockRunnerControllers.EXPECT().
		CreateRunnerController(gomock.Any(), gomock.Any()).
		Return(&gitlab.RunnerController{
			ID:          42,
			Description: "Test Controller",
			State:       gitlab.RunnerControllerStateEnabled,
			CreatedAt:   &fixedTime,
			UpdatedAt:   &fixedTime,
		}, nil, nil)

	out, err := exec("--description 'Test Controller' --state enabled")
	require.NoError(t, err)
	assert.Equal(t, "Created runner controller 42\n", out.OutBuf.String())
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
	tc.MockRunnerControllers.EXPECT().
		CreateRunnerController(gomock.Any(), gomock.Any()).
		Return(&gitlab.RunnerController{
			ID:          42,
			Description: "Test Controller",
			State:       gitlab.RunnerControllerStateEnabled,
			CreatedAt:   &fixedTime,
			UpdatedAt:   &fixedTime,
		}, nil, nil)

	out, err := exec("--output json")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), `"id":42`)
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

	tc.MockRunnerControllers.EXPECT().
		CreateRunnerController(gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("API error"))

	_, err := exec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}
