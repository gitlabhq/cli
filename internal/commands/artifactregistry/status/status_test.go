//go:build !integration

package status

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/artifactregistrytest"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// wireRequest mirrors the JSON body `glab artifact-registry status` is expected to send to
// POST /api/v4/token_exchange, via artifactregistry.
type wireRequest = artifactregistrytest.WireRequest

// newTestServer starts a fake token_exchange endpoint and returns it along
// with a counter of how many requests it received.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return artifactregistrytest.NewTestServer(t, handler)
}

// newTestExec wires NewCmd to an api.Client pointed at srv, mirroring how
// f.ApiClient(hostname).Lab() resolves in production.
func newTestExec(t *testing.T, srv *httptest.Server) cmdtest.CmdExecFunc {
	t.Helper()
	return artifactregistrytest.NewTestExec(t, srv, NewCmd)
}

// makeJWT mints a JWT carrying claims. The command never verifies token
// signatures, so any signing key works here.
func makeJWT(t *testing.T, claims jwt.RegisteredClaims) string {
	t.Helper()
	return artifactregistrytest.MakeJWT(t, claims)
}

func TestStatus_Success(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/42",
		Audience:  jwt.ClaimStrings{"gitlab-artifact-registry"},
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody wireRequest
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/token_exchange", r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})
	exec := newTestExec(t, srv)

	out, err := exec("")
	require.NoError(t, err)

	assert.EqualValues(t, 1, count.Load())
	// status reads the token for its claims and discards it, so it must not
	// send expires_in: the server's own default lifetime applies.
	assert.Equal(t, wireRequest{Audience: "gitlab-artifact-registry"}, gotBody)

	output := out.OutBuf.String()
	assert.Contains(t, output, "https://gitlab.example.com")
	assert.Contains(t, output, "gid://gitlab/User/42")
	assert.Contains(t, output, "gitlab-artifact-registry")
	assert.Contains(t, output, exp.Format(time.RFC3339))
	assert.NotContains(t, output, wantToken, "the raw bearer token must never reach stdout")
}

func TestStatus_JSONOutput(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/42",
		Audience:  jwt.ClaimStrings{"gitlab-artifact-registry"},
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})
	exec := newTestExec(t, srv)

	out, err := exec("--output json")
	require.NoError(t, err)

	var result struct {
		Issuer    string    `json:"issuer"`
		Subject   string    `json:"subject"`
		Audience  string    `json:"audience"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, "https://gitlab.example.com", result.Issuer)
	assert.Equal(t, "gid://gitlab/User/42", result.Subject)
	assert.Equal(t, "gitlab-artifact-registry", result.Audience)
	assert.WithinDuration(t, exp, result.ExpiresAt, 0)
	assert.NotContains(t, out.OutBuf.String(), wantToken, "the raw bearer token must never reach stdout")
}

func TestStatus_ValidHostnameReachesTheExchange(t *testing.T) {
	// The counterpart to TestStatus_InvalidHostname: a stricter validator must
	// not start rejecting legitimate hosts unnoticed.
	tests := []struct {
		name string
		args string
	}{
		{name: "bare hostname", args: "--hostname gitlab.example.com"},
		// Empty means "use the configured instance", so it is a valid value even
		// when passed explicitly.
		{name: "explicitly empty", args: "--hostname ''"},
		{name: "not passed at all", args: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantToken := makeJWT(t, jwt.RegisteredClaims{
				Issuer:    "https://gitlab.example.com",
				Subject:   "gid://gitlab/User/42",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			})

			srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
			})
			exec := newTestExec(t, srv)

			_, err := exec(tc.args)
			require.NoError(t, err)
			assert.EqualValues(t, 1, count.Load())
		})
	}
}

func TestStatus_InvalidHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
	}{
		{name: "URL rather than hostname", hostname: "https://gitlab.example.com"},
		{name: "hostname with a port", hostname: "gitlab.example.com:8080"},
		{name: "hostname with a path", hostname: "gitlab.example.com/foo"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request to fake server: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			})
			exec := newTestExec(t, srv)

			_, err := exec("--hostname " + tc.hostname)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "error parsing --hostname")
			// The credential must not leave the machine before the host it would
			// be sent to has been checked.
			assert.EqualValues(t, 0, count.Load(), "an invalid --hostname must be rejected before any HTTP call")
		})
	}
}

func TestStatus_ServerError(t *testing.T) {
	// 400 is the real non-2xx surface for a bad request (the other
	// documented codes are 401, 404, and 429).
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"audience is not a valid value"}`))
	})
	exec := newTestExec(t, srv)

	_, err := exec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audience is not a valid value")
}

func TestStatus_TokenExchangeNotEnabled(t *testing.T) {
	// When gate_token_exchange_endpoint is disabled on the instance, the
	// endpoint itself is absent, so the server responds 404 rather than a
	// feature-specific error.
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	exec := newTestExec(t, srv)

	_, err := exec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token exchange is not enabled on this instance")
}
