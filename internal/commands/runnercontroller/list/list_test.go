//go:build !integration

package list

import (
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
	tc.MockRunnerControllers.EXPECT().
		ListRunnerControllers(gomock.Any(), gomock.Any()).
		Return([]*gitlab.RunnerController{
			{ID: 1, Description: "controller-1", State: gitlab.RunnerControllerStateEnabled, CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
		}, nil, nil)

	out, err := exec("")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		ID%[1]sDescription%[1]sState%[1]sCreated At%[1]sUpdated At
		1%[1]scontroller-1%[1]senabled%[1]s%[2]s%[1]s%[2]s
	`, "\t", fixedTime)
	assert.Equal(t, expectedOutput, out.OutBuf.String())
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
	tc.MockRunnerControllers.EXPECT().
		ListRunnerControllers(gomock.Any(), gomock.Any()).
		Return([]*gitlab.RunnerController{
			{ID: 1, Description: "controller-1", State: gitlab.RunnerControllerStateEnabled, CreatedAt: &fixedTime, UpdatedAt: &fixedTime},
		}, nil, nil)

	out, err := exec("--output json")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), `"id":1`)
	assert.Contains(t, out.OutBuf.String(), `"description":"controller-1"`)
}
