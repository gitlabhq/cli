//go:build !integration

package list

import (
	"encoding/json"
	"errors"
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

func TestList(t *testing.T) {
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
		ListRunnerControllerScopes(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerScopes{
			InstanceLevelScopings: []*gitlab.RunnerControllerInstanceLevelScoping{
				{CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
			},
			RunnerLevelScopings: []*gitlab.RunnerControllerRunnerLevelScoping{},
		}, nil, nil)

	out, err := exec("42")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		Scope Type%[1]sRunner ID%[1]sCreated At%[1]sUpdated At
		instance%[1]s-%[1]s%[2]s%[1]s%[2]s
	`, "\t", fixedTime)
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestListEmpty(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		ListRunnerControllerScopes(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerScopes{
			InstanceLevelScopings: []*gitlab.RunnerControllerInstanceLevelScoping{},
			RunnerLevelScopings:   []*gitlab.RunnerControllerRunnerLevelScoping{},
		}, nil, nil)

	out, err := exec("42")
	require.NoError(t, err)
	assert.Equal(t, "No scopes configured for runner controller 42\n", out.OutBuf.String())
}

func TestListJSON(t *testing.T) {
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
		ListRunnerControllerScopes(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerScopes{
			InstanceLevelScopings: []*gitlab.RunnerControllerInstanceLevelScoping{
				{CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
			},
			RunnerLevelScopings: []*gitlab.RunnerControllerRunnerLevelScoping{},
		}, nil, nil)

	out, err := exec("42 --output json")
	require.NoError(t, err)

	var result gitlab.RunnerControllerScopes
	err = json.Unmarshal(out.OutBuf.Bytes(), &result)
	require.NoError(t, err)

	require.Len(t, result.InstanceLevelScopings, 1)
	assert.Equal(t, fixedTime, *result.InstanceLevelScopings[0].CreatedAt)
	assert.Equal(t, fixedTime, *result.InstanceLevelScopings[0].UpdatedAt)
}

func TestListRunnerLevelScopes(t *testing.T) {
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
		ListRunnerControllerScopes(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerScopes{
			InstanceLevelScopings: []*gitlab.RunnerControllerInstanceLevelScoping{},
			RunnerLevelScopings: []*gitlab.RunnerControllerRunnerLevelScoping{
				{RunnerID: 5, CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
			},
		}, nil, nil)

	out, err := exec("42")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		Scope Type%[1]sRunner ID%[1]sCreated At%[1]sUpdated At
		runner%[1]s5%[1]s%[2]s%[1]s%[2]s
	`, "\t", fixedTime)
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestListMixedScopes(t *testing.T) {
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
		ListRunnerControllerScopes(int64(42), gomock.Any()).
		Return(&gitlab.RunnerControllerScopes{
			InstanceLevelScopings: []*gitlab.RunnerControllerInstanceLevelScoping{
				{CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
			},
			RunnerLevelScopings: []*gitlab.RunnerControllerRunnerLevelScoping{
				{RunnerID: 5, CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
				{RunnerID: 10, CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
			},
		}, nil, nil)

	out, err := exec("42")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		Scope Type%[1]sRunner ID%[1]sCreated At%[1]sUpdated At
		instance%[1]s-%[1]s%[2]s%[1]s%[2]s
		runner%[1]s5%[1]s%[2]s%[1]s%[2]s
		runner%[1]s10%[1]s%[2]s%[1]s%[2]s
	`, "\t", fixedTime)
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestListError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		ListRunnerControllerScopes(int64(42), gomock.Any()).
		Return(nil, nil, errors.New("API error"))

	_, err := exec("42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestListInvalidID(t *testing.T) {
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
