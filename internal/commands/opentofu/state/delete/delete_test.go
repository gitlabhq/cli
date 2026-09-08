//go:build !integration

package delete

import (
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDelete_EntireState(t *testing.T) {
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
		Delete("OWNER/REPO", "production", gomock.Any()).
		Return(nil, nil)

	// WHEN
	out, err := exec("production --force")
	require.NoError(t, err)

	// THEN
	assert.Equal(t, "Deleted state production\n", out.OutBuf.String())
}

func TestDelete_Serial(t *testing.T) {
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
		DeleteVersion("OWNER/REPO", "production", uint64(42), gomock.Any()).
		Return(nil, nil)

	// WHEN
	out, err := exec("production 42 --force")
	require.NoError(t, err)

	// THEN
	assert.Equal(t, "Deleted version with serial 42 of state production\n", out.OutBuf.String())
}

func TestDelete_YesInPrompt(t *testing.T) {
	// GIVEN
	tc := gitlabtesting.NewTestClient(t)

	c := ugh.New(t)
	c.Expect(ugh.Confirm("Are you sure you want to delete? This action is destructive")).
		Do(ugh.Affirm)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		cmdtest.WithConsole(t, c),
	)

	// setup mock expectations
	tc.MockTerraformStates.EXPECT().
		Delete("OWNER/REPO", "production", gomock.Any()).
		Return(nil, nil)

	// WHEN
	out, err := exec("production")
	require.NoError(t, err)

	// THEN
	assert.Contains(t, out.OutBuf.String(), "Deleted state production\n")
}

func TestDelete_NoInPrompt(t *testing.T) {
	// GIVEN
	tc := gitlabtesting.NewTestClient(t)

	c := ugh.New(t)
	c.Expect(ugh.Confirm("Are you sure you want to delete? This action is destructive")).
		Do(ugh.Reject)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		cmdtest.WithConsole(t, c),
	)

	// setup mock expectations
	tc.MockTerraformStates.EXPECT().
		Delete("OWNER/REPO", "production", gomock.Any()).
		Times(0)

	// WHEN
	_, err := exec("production")
	require.ErrorIs(t, err, errAbort)
}
