// Package artifactregistrytest provides shared test helpers for
// internal/api/artifactregistry and the commands built on top of it, so their
// tests don't each redefine the same fake token_exchange server and fake JWT.
package artifactregistrytest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// WireRequest mirrors the JSON body artifactregistry.Client sends to
// POST /api/v4/token_exchange.
type WireRequest struct {
	Audience  string `json:"audience"`
	ExpiresIn int    `json:"expires_in"`
}

// NewTestServer starts a fake token_exchange endpoint and returns it along
// with a counter of how many requests it received, so tests can assert that
// invalid input never reaches the network.
func NewTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	return srv, &count
}

// NewTokenExchangeServer starts a fake token_exchange endpoint that asserts
// the request is a well-formed POST to the expected path, decodes its body
// into gotBody, and always responds with wantToken.
func NewTokenExchangeServer(t *testing.T, wantToken string, gotBody *WireRequest) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	return NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/token_exchange", r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(gotBody))

		body, err := json.Marshal(struct { //nolint:forbidigo // building the fake server's response body, not stdout output
			Token string `json:"token"`
		}{Token: wantToken})
		if !assert.NoError(t, err) {
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

// NewTestClient builds a *gitlab.Client pointed at baseURL (typically an
// httptest.Server's URL).
func NewTestClient(t *testing.T, baseURL string) *gitlab.Client {
	t.Helper()

	gl, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(baseURL+"/api/v4"))
	require.NoError(t, err)

	return gl
}

// NewTestExec wires cmdFunc to an api.Client pointed at srv, mirroring how
// f.ApiClient(hostname).Lab() resolves in production.
func NewTestExec(t *testing.T, srv *httptest.Server, cmdFunc cmdtest.CmdFunc) cmdtest.CmdExecFunc {
	t.Helper()

	apiClient := cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(NewTestClient(t, srv.URL)))
	return cmdtest.SetupCmdForTest(t, cmdFunc, false, cmdtest.WithApiClient(apiClient))
}

// MakeJWT mints a JWT carrying claims. Code under test never verifies token
// signatures, so any signing key works here.
func MakeJWT(t *testing.T, claims jwt.RegisteredClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-signing-key"))
	require.NoError(t, err)

	return signed
}
