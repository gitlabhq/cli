//go:build !integration

package revoke

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRevoke_InvalidAgentID(t *testing.T) {
	t.Parallel()

	// GIVEN
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	// WHEN
	_, err := exec("this-is-not-an-agent-id 42")

	// THEN
	assert.EqualError(t, err, `agent ID must be a valid integer, got "this-is-not-an-agent-id"`)
}

func TestRevoke_InvalidTokenID(t *testing.T) {
	t.Parallel()

	// GIVEN
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	// WHEN
	_, err := exec("42 this-is-not-a-token-id")

	// THEN
	assert.EqualError(t, err, `token ID must be a valid integer, got "this-is-not-a-token-id"`)
}

func TestRevoke_Success(t *testing.T) {
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
		RevokeAgentToken("OWNER/REPO", int64(1), int64(2), gomock.Any()).
		Return(nil, nil)

	// WHEN
	_, err := exec("1 2")

	// THEN
	assert.NoError(t, err)
}

func TestRevoke_Error(t *testing.T) {
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
		RevokeAgentToken("OWNER/REPO", int64(1), int64(2), gomock.Any()).
		Return(nil, errors.New("dummy error"))

	// WHEN
	_, err := exec("1 2")

	// THEN
	assert.EqualError(t, err, "failed to revoke token: dummy error")
}
