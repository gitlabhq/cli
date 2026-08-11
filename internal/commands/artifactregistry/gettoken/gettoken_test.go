//go:build !integration

package gettoken

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

	"gitlab.com/gitlab-org/cli/internal/api/artifactregistry"
	"gitlab.com/gitlab-org/cli/internal/testing/artifactregistrytest"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// wireRequest mirrors the JSON body `glab artifact-registry get-token` is expected to send
// to POST /api/v4/token_exchange, via artifactregistry.
type wireRequest = artifactregistrytest.WireRequest

// newTestServer starts a fake token_exchange endpoint and returns it along
// with a counter of how many requests it received, so tests can assert that
// invalid input (e.g. an out-of-range --duration) never reaches the network.
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

// tokenServer starts a fake token_exchange endpoint that always returns
// wantToken, decoding each request body into gotBody.
func tokenServer(t *testing.T, wantToken string, gotBody *wireRequest) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return artifactregistrytest.NewTokenExchangeServer(t, wantToken, gotBody)
}

func TestGetToken_DefaultDuration(t *testing.T) {
	exp := time.Now().Add(artifactregistry.DefaultDuration).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody wireRequest
	srv, count := tokenServer(t, wantToken, &gotBody)
	exec := newTestExec(t, srv)

	out, err := exec("")
	require.NoError(t, err)

	assert.EqualValues(t, 1, count.Load())
	assert.Equal(t, wireRequest{Audience: "gitlab-artifact-registry", ExpiresIn: int(artifactregistry.DefaultDuration.Seconds())}, gotBody)
	assert.Equal(t, wantToken+"\n", out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestGetToken_CustomDuration(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody wireRequest
	srv, count := tokenServer(t, wantToken, &gotBody)
	exec := newTestExec(t, srv)

	out, err := exec("--duration 1h")
	require.NoError(t, err)

	assert.EqualValues(t, 1, count.Load())
	assert.Equal(t, 3600, gotBody.ExpiresIn)
	assert.Equal(t, wantToken+"\n", out.OutBuf.String())
}

func TestGetToken_DurationOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{name: "below the 1-second floor", args: "--duration 500ms"},
		{name: "negative duration", args: "--duration=-1s"},
		{name: "above the 12-hour ceiling", args: "--duration 13h"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request to fake server: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			})
			exec := newTestExec(t, srv)

			out, err := exec(tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "error parsing --duration")
			assert.EqualValues(t, 0, count.Load(), "an out-of-range duration must be rejected before making an HTTP call")
			assert.Empty(t, out.OutBuf.String())
		})
	}
}

func TestGetToken_JSONOutput(t *testing.T) {
	exp := time.Now().Add(artifactregistry.DefaultDuration).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody wireRequest
	srv, _ := tokenServer(t, wantToken, &gotBody)
	exec := newTestExec(t, srv)

	out, err := exec("--output json")
	require.NoError(t, err)

	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	assert.Equal(t, wantToken, result.Token)
	assert.WithinDuration(t, exp, result.ExpiresAt, 0)
}

func TestGetToken_ServerError(t *testing.T) {
	// 400 is the real non-2xx surface for a bad request (the other
	// documented codes are 401, 404, and 429).
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"audience is not a valid value"}`))
	})
	exec := newTestExec(t, srv)

	out, err := exec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audience is not a valid value")
	assert.Empty(t, out.OutBuf.String())
}

func TestGetToken_TokenExchangeNotEnabled(t *testing.T) {
	// When gate_token_exchange_endpoint is disabled on the instance, the
	// endpoint itself is absent, so the server responds 404 rather than a
	// feature-specific error.
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	exec := newTestExec(t, srv)

	out, err := exec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token exchange is not enabled on this instance")
	assert.Empty(t, out.OutBuf.String())
}

func TestGetToken_InvalidHostname(t *testing.T) {
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

			out, err := exec("--hostname " + tc.hostname)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "error parsing --hostname")
			// The credential must not leave the machine before the host it would
			// be sent to has been checked.
			assert.EqualValues(t, 0, count.Load(), "an invalid --hostname must be rejected before any HTTP call")
			assert.Empty(t, out.OutBuf.String())
		})
	}
}

func TestGetToken_ValidHostnameReachesTheExchange(t *testing.T) {
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
				Subject:   "gid://gitlab/User/1",
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

// TestGetToken_StdoutCarriesOnlyTheToken asserts the negative half of the
// capture contract: a failed exchange must not leave a partial or empty line
// on stdout for `TOKEN=$(glab artifact-registry get-token)` to capture. The
// happy path is covered by TestGetToken_DefaultDuration.
func TestGetToken_StdoutCarriesOnlyTheToken(t *testing.T) {
	// 400, not 500: a 5xx triggers the API client's retry/backoff, which
	// would make this test slow for no benefit — any failed exchange, whatever
	// the status, must leave stdout empty.
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	exec := newTestExec(t, srv)

	out, err := exec("")
	require.Error(t, err)

	assert.Empty(t, out.OutBuf.String())
}
