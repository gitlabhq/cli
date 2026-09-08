//go:build !integration

package delete

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

func TestDeleteInstanceScope(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		RemoveRunnerControllerInstanceScope(int64(42), gomock.Any()).
		Return(nil, nil)

	out, err := exec("42 --instance --force")
	require.NoError(t, err)
	assert.Equal(t, "Removed instance-level scope from runner controller 42\n", out.OutBuf.String())
}

func TestDeleteInstanceScopeError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		RemoveRunnerControllerInstanceScope(int64(42), gomock.Any()).
		Return(nil, errors.New("API error"))

	_, err := exec("42 --instance --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestDeleteRunnerScope(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		RemoveRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
		Return(nil, nil)

	out, err := exec("42 --runner 5 --force")
	require.NoError(t, err)
	assert.Equal(t, "Removed runner-level scope for runner 5 from runner controller 42\n", out.OutBuf.String())
}

func TestDeleteMultipleRunnerScopes(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	gomock.InOrder(
		tc.MockRunnerControllerScopes.EXPECT().
			RemoveRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
			Return(nil, nil),
		tc.MockRunnerControllerScopes.EXPECT().
			RemoveRunnerControllerRunnerScope(int64(42), int64(10), gomock.Any()).
			Return(nil, nil),
	)

	out, err := exec("42 --runner 5 --runner 10 --force")
	require.NoError(t, err)
	assert.Equal(t, "Removed runner-level scope for runner 5 from runner controller 42\nRemoved runner-level scope for runner 10 from runner controller 42\n", out.OutBuf.String())
}

func TestDeleteMultipleRunnerScopesCommaSeparated(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	gomock.InOrder(
		tc.MockRunnerControllerScopes.EXPECT().
			RemoveRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
			Return(nil, nil),
		tc.MockRunnerControllerScopes.EXPECT().
			RemoveRunnerControllerRunnerScope(int64(42), int64(10), gomock.Any()).
			Return(nil, nil),
	)

	out, err := exec("42 --runner 5,10 --force")
	require.NoError(t, err)
	assert.Equal(t, "Removed runner-level scope for runner 5 from runner controller 42\nRemoved runner-level scope for runner 10 from runner controller 42\n", out.OutBuf.String())
}

func TestDeleteMultipleRunnerScopesPartialError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	gomock.InOrder(
		tc.MockRunnerControllerScopes.EXPECT().
			RemoveRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
			Return(nil, nil),
		tc.MockRunnerControllerScopes.EXPECT().
			RemoveRunnerControllerRunnerScope(int64(42), int64(10), gomock.Any()).
			Return(nil, errors.New("API error")),
	)

	_, err := exec("42 --runner 5,10 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestDeleteRunnerScopeError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunnerControllerScopes.EXPECT().
		RemoveRunnerControllerRunnerScope(int64(42), int64(5), gomock.Any()).
		Return(nil, errors.New("API error"))

	_, err := exec("42 --runner 5 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
}

func TestDeleteRequiresScopeType(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags in the group [instance runner] is required")
}

func TestDeleteMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42 --instance --runner 5 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [instance runner] are set none of the others can be")
}

func TestDeleteRequiresForceNonInteractive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42 --instance")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force required")
}

func TestDeleteRunnerScopeRequiresForceNonInteractive(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("42 --runner 5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force required")
}

func TestDeleteInvalidID(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("invalid --instance --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}
