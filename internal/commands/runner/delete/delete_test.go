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

func TestDelete(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunners.EXPECT().
		DeleteRegisteredRunnerByID(int64(6), gomock.Any()).
		Return(nil, nil)

	out, err := exec("6 --force")
	require.NoError(t, err)
	assert.Equal(t, "Deleted runner 6\n", out.OutBuf.String())
}

func TestDeleteError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunners.EXPECT().
		DeleteRegisteredRunnerByID(int64(6), gomock.Any()).
		Return(nil, errors.New("API error"))

	_, err := exec("6 --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
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

	_, err := exec("invalid --force")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
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

	_, err := exec("6")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force required")
}
