//go:build !integration

package get_token

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.uber.org/mock/gomock"
	clientauthenticationv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestAgentGetToken(t *testing.T) {
	// GIVEN
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmdAgentGetToken, false, cmdtest.WithGitLabClient(tc.Client))

	tc.MockUsers.EXPECT().
		CreatePersonalAccessTokenForCurrentUser(gomock.Any(), gomock.Any()).
		Return(&gitlab.PersonalAccessToken{
			Token:     "glpat-XTESTX",
			ExpiresAt: new(mustParse(t, "2023-01-02")),
		}, &gitlab.Response{}, nil).
		Times(1)

	// WHEN
	output, err := exec("--agent 42")
	if err != nil {
		t.Errorf("error running command `cluster agent get-token --agent 42`: %v", err)
	}

	assert.JSONEq(t, `{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","spec":{"interactive":false},"status":{"expirationTimestamp":"2023-01-01T23:55:00Z","token":"pat:42:glpat-XTESTX"}}`+"\n", output.String())
	assert.Empty(t, output.Stderr())
}

// TestAgentGetToken_OutputIsValidExecCredential round-trips the command's
// JSON output through the upstream clientauthenticationv1.ExecCredential type
// that kubectl uses to consume client-go credential plugins. This is a
// schema-level regression guard for the compact-JSON output format: if a
// future change broke the shape that kubectl expects, the unmarshal would
// fail or produce zero values for required fields.
func TestAgentGetToken_OutputIsValidExecCredential(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmdAgentGetToken, false, cmdtest.WithGitLabClient(tc.Client))

	tc.MockUsers.EXPECT().
		CreatePersonalAccessTokenForCurrentUser(gomock.Any(), gomock.Any()).
		Return(&gitlab.PersonalAccessToken{
			Token:     "glpat-XTESTX",
			ExpiresAt: new(mustParse(t, "2023-01-02")),
		}, &gitlab.Response{}, nil).
		Times(1)

	output, err := exec("--agent 42")
	require.NoError(t, err)

	var ec clientauthenticationv1.ExecCredential
	require.NoError(t, json.Unmarshal(output.OutBuf.Bytes(), &ec))

	assert.Equal(t, "ExecCredential", ec.Kind)
	assert.Equal(t, "client.authentication.k8s.io/v1", ec.APIVersion)
	require.NotNil(t, ec.Status)
	assert.Equal(t, "pat:42:glpat-XTESTX", ec.Status.Token)
	require.NotNil(t, ec.Status.ExpirationTimestamp)
	assert.False(t, ec.Status.ExpirationTimestamp.IsZero())
}

func mustParse(t *testing.T, dt string) gitlab.ISOTime {
	t.Helper()

	x, err := time.Parse(time.DateOnly, dt)
	require.NoError(t, err)
	return gitlab.ISOTime(x)
}
