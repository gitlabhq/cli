//go:build !integration

package get

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestGet(t *testing.T) {
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
		GetRunnerController(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerDetails{
			RunnerController: gitlab.RunnerController{
				ID:          42,
				Description: "my-controller",
				State:       gitlab.RunnerControllerStateEnabled,
				CreatedAt:   &fixedTime,
				UpdatedAt:   &fixedTime,
			},
			Connected: true,
		}, nil, nil)

	out, err := exec("42")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		ID	42
		Description	my-controller
		State	enabled
		Connected	yes
		Created At	%[1]s
		Updated At	%[1]s
	`, fixedTime)
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestGetDisconnected(t *testing.T) {
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
		GetRunnerController(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerDetails{
			RunnerController: gitlab.RunnerController{
				ID:          42,
				Description: "offline-controller",
				State:       gitlab.RunnerControllerStateDisabled,
				CreatedAt:   &fixedTime,
				UpdatedAt:   &fixedTime,
			},
			Connected: false,
		}, nil, nil)

	out, err := exec("42")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		ID	42
		Description	offline-controller
		State	disabled
		Connected	no
		Created At	%[1]s
		Updated At	%[1]s
	`, fixedTime)
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestGetJSON(t *testing.T) {
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
		GetRunnerController(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerDetails{
			RunnerController: gitlab.RunnerController{
				ID:          42,
				Description: "my-controller",
				State:       gitlab.RunnerControllerStateEnabled,
				CreatedAt:   &fixedTime,
				UpdatedAt:   &fixedTime,
			},
			Connected: true,
		}, nil, nil)

	out, err := exec("42 --output json")
	require.NoError(t, err)

	var result gitlab.RunnerControllerDetails
	err = json.Unmarshal(out.OutBuf.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, int64(42), result.ID)
	assert.Equal(t, "my-controller", result.Description)
	assert.Equal(t, gitlab.RunnerControllerStateEnabled, result.State)
	assert.True(t, result.Connected)
	assert.Equal(t, fixedTime, *result.CreatedAt)
	assert.Equal(t, fixedTime, *result.UpdatedAt)
}

func TestGetInvalidID(t *testing.T) {
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
