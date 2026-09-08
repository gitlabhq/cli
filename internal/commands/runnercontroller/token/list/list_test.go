//go:build !integration

package list

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
	lastUsedAt := time.Date(2025, 1, 15, 9, 0, 0, 0, time.UTC)
	tc.MockRunnerControllerTokens.EXPECT().
		ListRunnerControllerTokens(int64(42), gomock.Any(), gomock.Any()).
		Return([]*gitlab.RunnerControllerToken{
			{
				ID:                 1,
				RunnerControllerID: 42,
				Description:        "Token 1",
				LastUsedAt:         &lastUsedAt,
				CreatedAt:          &fixedTime,
				UpdatedAt:          &fixedTime,
			},
			{
				ID:                 2,
				RunnerControllerID: 42,
				Description:        "",
				CreatedAt:          &fixedTime,
				UpdatedAt:          &fixedTime,
			},
		}, nil, nil)

	out, err := exec("42")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		ID	Description	Last Used At	Created At	Updated At
		1	Token 1	%[2]s	%[1]s	%[1]s
		2	-	-	%[1]s	%[1]s
	`, fixedTime, lastUsedAt)
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
	lastUsedAt := time.Date(2025, 1, 15, 9, 0, 0, 0, time.UTC)
	tc.MockRunnerControllerTokens.EXPECT().
		ListRunnerControllerTokens(int64(42), gomock.Any(), gomock.Any()).
		Return([]*gitlab.RunnerControllerToken{
			{
				ID:                 1,
				RunnerControllerID: 42,
				Description:        "Token 1",
				LastUsedAt:         &lastUsedAt,
				CreatedAt:          &fixedTime,
				UpdatedAt:          &fixedTime,
			},
		}, nil, nil)

	out, err := exec("42 --output json")
	require.NoError(t, err)

	var result []*gitlab.RunnerControllerToken
	err = json.Unmarshal(out.OutBuf.Bytes(), &result)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].ID)
	assert.Equal(t, int64(42), result[0].RunnerControllerID)
	assert.Equal(t, "Token 1", result[0].Description)
	assert.Equal(t, lastUsedAt, *result[0].LastUsedAt)
	assert.Equal(t, fixedTime, *result[0].CreatedAt)
	assert.Equal(t, fixedTime, *result[0].UpdatedAt)
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
