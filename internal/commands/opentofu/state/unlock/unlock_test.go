//go:build !integration

package unlock

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestUnlock(t *testing.T) {
	// GIVEN
	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	// setup mock expectations
	tc.MockTerraformStates.EXPECT().
		Unlock("OWNER/REPO", "production", gomock.Any()).
		Return(nil, nil)

	// WHEN
	out, err := exec("production")
	require.NoError(t, err)

	// THEN
	assert.Equal(t, "Unlocked state production\n", out.OutBuf.String())
}
