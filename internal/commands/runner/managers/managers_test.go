//go:build !integration

package managers

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

func TestRunnerManagers_Success(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	createdAt, _ := time.Parse(time.RFC3339, "2024-06-09T11:12:02.507Z")
	contactedAt, _ := time.Parse(time.RFC3339, "2024-06-09T06:30:09.355Z")
	managersResponse := []*gitlab.RunnerManager{
		{
			ID:           1,
			SystemID:     "s_89e5e9956577",
			Version:      "16.11.1",
			Revision:     "535ced5f",
			Platform:     "linux",
			Architecture: "amd64",
			CreatedAt:    &createdAt,
			ContactedAt:  &contactedAt,
			IPAddress:    "127.0.0.1",
			Status:       "offline",
		},
	}

	tc.MockRunners.EXPECT().
		ListRunnerManagers(int64(1), gomock.Any()).
		Return(managersResponse, nil, nil)

	out, err := exec("1")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		Showing 1 manager on . (Page 1)

		ID%[1]sSystem ID%[1]sVersion%[1]sPlatform%[1]sArchitecture%[1]sIP Address%[1]sStatus
		1%[1]ss_89e5e9956577%[1]s16.11.1%[1]slinux%[1]samd64%[1]s127.0.0.1%[1]soffline

	`, "\t")
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestRunnerManagers_OutputJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	managersResponse := []*gitlab.RunnerManager{
		{
			ID:           2,
			SystemID:     "runner-2",
			Version:      "16.11.0",
			Platform:     "linux",
			Architecture: "amd64",
			Status:       "online",
		},
	}

	tc.MockRunners.EXPECT().
		ListRunnerManagers(int64(2), gomock.Any()).
		Return(managersResponse, nil, nil)

	out, err := exec("2 --output json")
	require.NoError(t, err)
	var decoded []*gitlab.RunnerManager
	err = json.Unmarshal(out.OutBuf.Bytes(), &decoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, int64(2), decoded[0].ID)
	assert.Equal(t, "runner-2", decoded[0].SystemID)
	assert.Equal(t, "online", decoded[0].Status)
}

func TestRunnerManagers_InvalidRunnerID(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestRunnerManagers_APIError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunners.EXPECT().
		ListRunnerManagers(int64(999), gomock.Any()).
		Return(nil, nil, errors.New("404 Runner Not Found"))

	_, err := exec("999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}
