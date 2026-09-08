//go:build !integration

package list

import (
	"errors"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestList_InvalidAgentID(t *testing.T) {
	t.Parallel()

	// GIVEN
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	// WHEN
	_, err := exec("this-is-not-an-agent-id")

	// THEN
	assert.EqualError(t, err, `agent ID must be a valid integer, got "this-is-not-an-agent-id"`)
}

func TestList_FailToRetrieveTokens(t *testing.T) {
	t.Parallel()

	// GIVEN
	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithGitLabClient(tc.Client),
	)

	// setup mock expectations
	tc.MockClusterAgents.EXPECT().
		ListAgentTokens("OWNER/REPO", int64(1), nil, gomock.Any()).
		Return(nil, nil, errors.New("dummy API failure"))

	// WHEN
	_, err := exec("1")

	// THEN
	assert.EqualError(t, err, "unable to retrieve agent tokens: dummy API failure")
}

func TestList_SingleToken(t *testing.T) {
	t.Parallel()

	// GIVEN
	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithGitLabClient(tc.Client),
	)

	// setup mock expectations
	gomock.InOrder(
		tc.MockClusterAgents.EXPECT().
			ListAgentTokens("OWNER/REPO", int64(1), nil, gomock.Any()).
			Return([]*gitlab.AgentToken{
				{
					ID:              42,
					Name:            "any-name",
					Description:     "any-description",
					AgentID:         1,
					Status:          "active",
					CreatedAt:       new(time.Time{}),
					CreatedByUserID: 100,
					// LastUsedAt:      &time.Time{},
				},
			}, nil, nil),
		tc.MockUsers.EXPECT().
			GetUser(int64(100), gomock.Any(), gomock.Any()).
			Return(&gitlab.User{Username: "any-username"}, nil, nil),
	)

	// WHEN
	out, err := exec("1")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		ID%[1]sName%[1]sStatus%[1]sCreated At%[1]sCreated By%[1]sLast Used At%[1]sDescription
		42%[1]sany-name%[1]sactive%[1]s0001-01-01T00:00:00Z%[1]sany-username%[1]snever%[1]sany-description
	`, "\t")

	// THEN
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestList_MultipleToken(t *testing.T) {
	t.Parallel()

	// GIVEN
	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithGitLabClient(tc.Client),
	)

	// setup mock expectations
	gomock.InOrder(
		tc.MockClusterAgents.EXPECT().
			ListAgentTokens("OWNER/REPO", int64(1), nil, gomock.Any()).
			Return([]*gitlab.AgentToken{
				{
					ID:              42,
					Name:            "any-name",
					Description:     "any-description",
					AgentID:         1,
					Status:          "active",
					CreatedAt:       new(time.Time{}),
					CreatedByUserID: 100,
					// LastUsedAt:      &time.Time{},
				},
				{
					ID:              84,
					Name:            "another-name",
					Description:     "another-description",
					AgentID:         1,
					Status:          "revoked",
					CreatedAt:       new(time.Time{}),
					CreatedByUserID: 100,
					// LastUsedAt:      &time.Time{},
				},
			}, nil, nil),
		tc.MockUsers.EXPECT().
			GetUser(int64(100), gomock.Any(), gomock.Any()).
			Return(&gitlab.User{Username: "any-username"}, nil, nil),
	)

	// WHEN
	out, err := exec("1")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		ID%[1]sName%[1]sStatus%[1]sCreated At%[1]sCreated By%[1]sLast Used At%[1]sDescription
		42%[1]sany-name%[1]sactive%[1]s0001-01-01T00:00:00Z%[1]sany-username%[1]snever%[1]sany-description
		84%[1]sanother-name%[1]srevoked%[1]s0001-01-01T00:00:00Z%[1]sany-username%[1]snever%[1]sanother-description
	`, "\t")

	// THEN
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestAgentTokenList_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithGitLabClient(tc.Client),
	)

	tc.MockClusterAgents.EXPECT().
		ListAgentTokens("OWNER/REPO", int64(1), nil, gomock.Any()).
		Return([]*gitlab.AgentToken{
			{
				ID:              42,
				Name:            "any-name",
				Description:     "any-description",
				AgentID:         1,
				Status:          "active",
				CreatedAt:       new(time.Time{}),
				CreatedByUserID: 100,
			},
		}, nil, nil)

	out, err := exec("1 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":42`)
	assert.Contains(t, out.String(), `"name":"any-name"`)
	assert.Contains(t, out.String(), `"status":"active"`)
	assert.Contains(t, out.String(), `"description":"any-description"`)
	assert.Empty(t, out.Stderr())
}
