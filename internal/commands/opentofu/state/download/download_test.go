//go:build !integration

package download

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDownload_Latest(t *testing.T) {
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
		DownloadLatest("OWNER/REPO", "production", gomock.Any()).
		Return(io.NopCloser(bytes.NewBufferString("hello world")), nil, nil)

	// WHEN
	out, err := exec("production")
	require.NoError(t, err)

	// THEN
	assert.Equal(t, "hello world", out.OutBuf.String())
}

func TestDownload_WithSerial(t *testing.T) {
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
		Download("OWNER/REPO", "production", uint64(42), gomock.Any()).
		Return(io.NopCloser(bytes.NewBufferString("hello world")), nil, nil)

	// WHEN
	out, err := exec("production 42")
	require.NoError(t, err)

	// THEN
	assert.Equal(t, "hello world", out.OutBuf.String())
}
