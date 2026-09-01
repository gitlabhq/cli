//go:build !integration

package credentialhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/oauth2"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func (errorResponseType) UnmarshalJSON(b []byte) error {
	if !bytes.Equal(b, []byte(`"error"`)) {
		return fmt.Errorf("error response type value must be '`error`' but was '%s'", b)
	}

	return nil
}

func (successResponseType) UnmarshalJSON(b []byte) error {
	if !bytes.Equal(b, []byte(`"success"`)) {
		return fmt.Errorf("success response type value must be '`success`' but was '%s'", b)
	}

	return nil
}

func TestCredentialHelper_PAT(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.AccessTokenAuthSource{Token: "any-pat"}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp response
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "https://gitlab.example.com", resp.InstanceURL)
	assert.Equal(t, "pat", resp.Token.Type)
	assert.Equal(t, "any-pat", resp.Token.Token)
	assert.True(t, resp.Token.ExpiryTimestamp.IsZero())
}

func TestCredentialHelper_JobToken(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.JobTokenAuthSource{Token: "CI_JOB_TOKEN_12345"}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp response
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "https://gitlab.example.com", resp.InstanceURL)
	assert.Equal(t, "job-token", resp.Token.Type)
	assert.Equal(t, "CI_JOB_TOKEN_12345", resp.Token.Token)
	assert.True(t, resp.Token.ExpiryTimestamp.IsZero())
	assert.JSONEq(t,
		`{"type":"success","instance_url":"https://gitlab.example.com","token":{"type":"job-token","token":"CI_JOB_TOKEN_12345"}}`,
		out.OutBuf.String(),
	)
}

func TestCredentialHelper_OAuth2AccessTokenOnly(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(accessTokenOnlyClient(t, "")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp response
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "https://gitlab.example.com", resp.InstanceURL)
	assert.Equal(t, "oauth2", resp.Token.Type)
	assert.Equal(t, "bare-access-token", resp.Token.Token)
	assert.True(t, resp.Token.ExpiryTimestamp.IsZero())

	// The struct cannot show that a zero expiry is dropped from the wire form.
	assert.JSONEq(t,
		`{"type":"success","instance_url":"https://gitlab.example.com","token":{"type":"oauth2","token":"bare-access-token"}}`,
		out.OutBuf.String(),
	)
}

func TestCredentialHelper_UnsupportedAuthSource(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, staticHeaderAuthSource{name: "X-Custom-Auth", value: "whatever"}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp errorResponse
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "unable to determine token", resp.Message)
}

// accessTokenOnlyClient builds a client the way NewClientFromConfig does for a
// host with is_oauth2 set and no refresh token, so the test covers the real
// construction rather than a hand-made auth source.
func accessTokenOnlyClient(t *testing.T, expiryDate string) *api.Client {
	t.Helper()

	cfg := "hosts:\n  gitlab.example.com:\n    is_oauth2: \"true\"\n    token: bare-access-token\n"
	if expiryDate != "" {
		cfg += "    oauth2_expiry_date: \"" + expiryDate + "\"\n"
	}

	client, err := api.NewClientFromConfig("gitlab.example.com", config.NewFromString(cfg), false, "test", api.WithoutTokenFromEnvironment())
	require.NoError(t, err)
	return client
}

// staticHeaderAuthSource stands in for an auth source this command does not
// know about.
type staticHeaderAuthSource struct {
	name  string
	value string
}

func (staticHeaderAuthSource) Init(context.Context, *gitlab.Client) error { return nil }

func (s staticHeaderAuthSource) Header(context.Context) (string, string, error) {
	return s.name, s.value, nil
}

func TestCredentialHelper_OAuth2(t *testing.T) {
	t.Parallel()

	expiryTime := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)

	ctrl := gomock.NewController(t)
	mockTokenSource := NewMockTokenSource(ctrl)
	mockTokenSource.EXPECT().Token().Return(&oauth2.Token{
		AccessToken: "oauth2-access-token",
		Expiry:      expiryTime,
	}, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
			hosts:
			  gitlab.example.com:
			    is_oauth2: "true"
			    token: old-token
		`))),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.OAuthTokenSource{TokenSource: mockTokenSource}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp response
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "https://gitlab.example.com", resp.InstanceURL)
	assert.Equal(t, "oauth2", resp.Token.Type)
	assert.Equal(t, "oauth2-access-token", resp.Token.Token)
	assert.Equal(t, expiryTime.UTC(), resp.Token.ExpiryTimestamp.UTC().Truncate(time.Second))
}

func TestCredentialHelper_OAuth2_RefreshError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockTokenSource := NewMockTokenSource(ctrl)
	mockTokenSource.EXPECT().Token().Return(nil, errors.New("token refresh failed"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
			hosts:
			  gitlab.example.com:
			    is_oauth2: "true"
		`))),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.OAuthTokenSource{TokenSource: mockTokenSource}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp errorResponse
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Contains(t, resp.Message, "gitlab.example.com", "the host must be named")
	assert.Contains(t, resp.Message, "failed to refresh the OAuth2 token")
	assert.Contains(t, resp.Message, "token refresh failed", "the underlying cause must survive")
}

func TestCredentialHelper_BaseRepoError(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepoError(errors.New("not a git repository")),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.AccessTokenAuthSource{Token: "any-pat"}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp response
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "https://gitlab.example.com", resp.InstanceURL)
	assert.Equal(t, "pat", resp.Token.Type)
	assert.Equal(t, "any-pat", resp.Token.Token)
	assert.True(t, resp.Token.ExpiryTimestamp.IsZero())
}

func TestCredentialHelper_ApiClientUnauthenticated(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.Unauthenticated{}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp errorResponse
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "glab is not authenticated. Use glab auth login to authenticate", resp.Message)
}

func TestCredentialHelper_NoToken(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp errorResponse
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "unable to determine token", resp.Message)
}

func TestCredentialHelper_RepoOverride(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.AccessTokenAuthSource{Token: "example-token"}, "gitlab.example.com")),
	)

	out, err := exec("--repo gitlab.example.com/owner/repo")

	require.NoError(t, err)

	var resp response
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "https://gitlab.example.com", resp.InstanceURL)
	assert.Equal(t, "pat", resp.Token.Type)
	assert.Equal(t, "example-token", resp.Token.Token)
}

// PasswordCredentialsAuthSource embeds a nil gitlab.AuthSource until Init, so
// its Header dereferences nil.
func TestCredentialHelper_UninitialisedAuthSource(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, &gitlab.PasswordCredentialsAuthSource{Password: "a-password"}, "gitlab.example.com")),
	)

	out, err := exec("")

	require.NoError(t, err)

	var resp errorResponse
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &resp))
	assert.Equal(t, "unable to determine token", resp.Message)
}

func TestCredentialHelper_OAuth2AccessTokenOnlyReportsStoredExpiry(t *testing.T) {
	t.Parallel()

	expiry := time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithApiClient(accessTokenOnlyClient(t, expiry.Format(time.RFC3339))),
	)

	out, err := exec("")

	require.NoError(t, err)
	assert.JSONEq(t,
		`{"type":"success","instance_url":"https://gitlab.example.com","token":{"type":"oauth2","token":"bare-access-token","expiry_timestamp":"2099-01-02T03:04:05Z"}}`,
		out.OutBuf.String(),
	)
}
