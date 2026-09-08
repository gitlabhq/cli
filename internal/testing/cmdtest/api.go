package cmdtest

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

func NewTestApiClient(t *testing.T, httpClient *http.Client, token, host string, options ...api.ClientOption) *api.Client {
	t.Helper()

	opts := []api.ClientOption{
		api.WithUserAgent("glab test client"),
		api.WithBaseURL(glinstance.APIEndpoint(host, glinstance.DefaultProtocol, "", "")),
		api.WithInsecureSkipVerify(true),
		api.WithHTTPClient(httpClient),
	}
	opts = append(opts, options...)
	testClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) { return gitlab.AccessTokenAuthSource{Token: token}, nil },
		opts...,
	)
	require.NoError(t, err)
	return testClient
}

func NewTestAuthSourceApiClient(t *testing.T, httpClient *http.Client, authSource gitlab.AuthSource, host string, options ...api.ClientOption) *api.Client {
	t.Helper()

	opts := []api.ClientOption{
		api.WithUserAgent("glab test client"),
		api.WithBaseURL(glinstance.APIEndpoint(host, glinstance.DefaultProtocol, "", "")),
		api.WithInsecureSkipVerify(true),
		api.WithHTTPClient(httpClient),
	}
	opts = append(opts, options...)
	testClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return authSource, nil
		},
		opts...,
	)
	require.NoError(t, err)
	return testClient
}
