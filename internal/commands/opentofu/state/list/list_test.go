//go:build !integration

package list

import (
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestList(t *testing.T) {
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
	now := time.Now().UTC()
	tc.MockTerraformStates.EXPECT().
		List("OWNER/REPO").
		Return([]gitlab.TerraformState{
			{
				Name: "any-name",
				LatestVersion: gitlab.TerraformStateVersion{
					Serial: 42,
				},
				CreatedAt: now.Add(-1 * time.Hour),
				UpdatedAt: now,
				LockedAt:  now.Add(-30 * time.Minute),
			},
		}, nil, nil)

	// WHEN
	out, err := exec("")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		Name%[1]sLatest Version Serial%[1]sCreated At%[1]sUpdated At%[1]sLocked At
		any-name%[1]s42%[1]s%[2]s%[1]s%[3]s%[1]s%[4]s
	`, "\t", now.Add(-1*time.Hour), now, now.Add(-30*time.Minute))

	// THEN
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestOpentofuStateList_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	now := time.Now().UTC()
	tc.MockTerraformStates.EXPECT().
		List("OWNER/REPO").
		Return([]gitlab.TerraformState{
			{
				Name: "test-state",
				LatestVersion: gitlab.TerraformStateVersion{
					Serial: 42,
				},
				CreatedAt: now.Add(-1 * time.Hour),
				UpdatedAt: now,
				LockedAt:  now.Add(-30 * time.Minute),
			},
		}, nil, nil)

	out, err := exec("--output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"name":"test-state"`)
	assert.Contains(t, out.String(), `"serial":42`)
	assert.Empty(t, out.Stderr())
}
