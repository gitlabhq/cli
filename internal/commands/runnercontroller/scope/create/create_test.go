//go:build !integration

package create

import (
	"encoding/json"
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

func TestCreateInstanceScope(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	tc.MockRunnerControllerScopes.EXPECT().
		AddRunnerControllerInstanceScope(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerInstanceLevelScoping{
			CreatedAt: &fixedTime,
			UpdatedAt: &fixedTime,
		}, nil, nil)

	out, err := exec("42 --instance")
	require.NoError(t, err)
	assert.Equal(t, "Added instance-level scope to runner controller 42\n", out.OutBuf.String())
}

func TestCreateInstanceScopeJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	tc.MockRunnerControllerScopes.EXPECT().
		AddRunnerControllerInstanceScope(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerInstanceLevelScoping{
			CreatedAt: &fixedTime,
			UpdatedAt: &fixedTime,
		}, nil, nil)

	out, err := exec("42 --instance --output json")
	require.NoError(t, err)

	var results []gitlab.RunnerControllerInstanceLevelScoping
	err = json.Unmarshal(out.OutBuf.Bytes(), &results)
	require.NoError(t, err)

	require.Len(t, results, 1)
	assert.Equal(t, fixedTime, *results[0].CreatedAt)
	assert.Equal(t, fixedTime, *results[0].UpdatedAt)
}

func TestCreateInstanceScopeError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		AddRunnerControllerInstanceScope(int64(42), gomock.Any()).
		Return(nil, nil, errors.New("API error"))

	_, err := exec("42 --instance")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestCreateRunnerScope(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	tc.MockRunnerControllerScopes.EXPECT().
		AddRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
		Return(&gitlab.RunnerControllerRunnerLevelScoping{
			RunnerID:  5,
			CreatedAt: &fixedTime,
			UpdatedAt: &fixedTime,
		}, nil, nil)

	out, err := exec("42 --runner 5")
	require.NoError(t, err)
	assert.Equal(t, "Added runner-level scope for runner 5 to runner controller 42\n", out.OutBuf.String())
}

func TestCreateRunnerScopeJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	tc.MockRunnerControllerScopes.EXPECT().
		AddRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
		Return(&gitlab.RunnerControllerRunnerLevelScoping{
			RunnerID:  5,
			CreatedAt: &fixedTime,
			UpdatedAt: &fixedTime,
		}, nil, nil)

	out, err := exec("42 --runner 5 --output json")
	require.NoError(t, err)

	var results []gitlab.RunnerControllerRunnerLevelScoping
	err = json.Unmarshal(out.OutBuf.Bytes(), &results)
	require.NoError(t, err)

	require.Len(t, results, 1)
	assert.Equal(t, int64(5), results[0].RunnerID)
	assert.Equal(t, fixedTime, *results[0].CreatedAt)
	assert.Equal(t, fixedTime, *results[0].UpdatedAt)
}

func TestCreateMultipleRunnerScopes(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	gomock.InOrder(
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
			Return(&gitlab.RunnerControllerRunnerLevelScoping{
				RunnerID:  5,
				CreatedAt: &fixedTime,
				UpdatedAt: &fixedTime,
			}, nil, nil),
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(10), gomock.Any()).
			Return(&gitlab.RunnerControllerRunnerLevelScoping{
				RunnerID:  10,
				CreatedAt: &fixedTime,
				UpdatedAt: &fixedTime,
			}, nil, nil),
	)

	out, err := exec("42 --runner 5 --runner 10")
	require.NoError(t, err)
	assert.Equal(t, "Added runner-level scope for runner 5 to runner controller 42\nAdded runner-level scope for runner 10 to runner controller 42\n", out.OutBuf.String())
}

func TestCreateMultipleRunnerScopesCommaSeparated(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	gomock.InOrder(
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
			Return(&gitlab.RunnerControllerRunnerLevelScoping{
				RunnerID:  5,
				CreatedAt: &fixedTime,
				UpdatedAt: &fixedTime,
			}, nil, nil),
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(10), gomock.Any()).
			Return(&gitlab.RunnerControllerRunnerLevelScoping{
				RunnerID:  10,
				CreatedAt: &fixedTime,
				UpdatedAt: &fixedTime,
			}, nil, nil),
	)

	out, err := exec("42 --runner 5,10")
	require.NoError(t, err)
	assert.Equal(t, "Added runner-level scope for runner 5 to runner controller 42\nAdded runner-level scope for runner 10 to runner controller 42\n", out.OutBuf.String())
}

func TestCreateMultipleRunnerScopesJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	gomock.InOrder(
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
			Return(&gitlab.RunnerControllerRunnerLevelScoping{
				RunnerID:  5,
				CreatedAt: &fixedTime,
				UpdatedAt: &fixedTime,
			}, nil, nil),
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(10), gomock.Any()).
			Return(&gitlab.RunnerControllerRunnerLevelScoping{
				RunnerID:  10,
				CreatedAt: &fixedTime,
				UpdatedAt: &fixedTime,
			}, nil, nil),
	)

	out, err := exec("42 --runner 5,10 --output json")
	require.NoError(t, err)

	var results []gitlab.RunnerControllerRunnerLevelScoping
	err = json.Unmarshal(out.OutBuf.Bytes(), &results)
	require.NoError(t, err)

	require.Len(t, results, 2)
	assert.Equal(t, int64(5), results[0].RunnerID)
	assert.Equal(t, int64(10), results[1].RunnerID)
}

func TestCreateMultipleRunnerScopesPartialError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	gomock.InOrder(
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
			Return(&gitlab.RunnerControllerRunnerLevelScoping{
				RunnerID:  5,
				CreatedAt: &fixedTime,
				UpdatedAt: &fixedTime,
			}, nil, nil),
		tc.MockRunnerControllerScopes.EXPECT().
			AddRunnerControllerRunnerScope(int64(42), int64(10), gomock.Any()).
			Return(nil, nil, errors.New("API error")),
	)

	_, err := exec("42 --runner 5,10")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestCreateRunnerScopeError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		AddRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
		Return(nil, nil, errors.New("API error"))

	_, err := exec("42 --runner 5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestCreateRequiresScopeType(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags in the group [instance runner] is required")
}

func TestCreateMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42 --instance --runner 5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [instance runner] are set none of the others can be")
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

	_, err := exec("invalid --instance")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}
