//go:build !integration

package artifactregistry

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

	"gitlab.com/gitlab-org/cli/internal/testing/tokenexchangetest"
)

// wireRequest mirrors the JSON body artifactregistry is expected to send to
// POST /api/v4/token_exchange.
type wireRequest = tokenexchangetest.WireRequest

// newTestServer starts a fake token_exchange endpoint and returns it along
// with a counter of how many requests it received, so tests can assert that
// invalid input never reaches the network.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return tokenexchangetest.NewTestServer(t, handler)
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClient(tokenexchangetest.NewTestClient(t, baseURL))
}

// makeJWT mints a JWT carrying claims. artifactregistry never verifies token
// signatures (it trusts the TLS connection to the GitLab host that issued
// the token), so any signing key works.
func makeJWT(t *testing.T, claims jwt.RegisteredClaims) string {
	t.Helper()
	return tokenexchangetest.MakeJWT(t, claims)
}

func TestExchangeToken_Success(t *testing.T) {
	exp := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		Audience:  jwt.ClaimStrings{"gitlab-artifact-registry", "gitlab-iam-data-access"},
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody wireRequest
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/token_exchange", r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	client := newTestClient(t, srv.URL)

	result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.EqualValues(t, 1, count.Load())
	assert.Equal(t, wireRequest{Audience: "gitlab-artifact-registry", ExpiresIn: 900}, gotBody)
	assert.Equal(t, wantToken, result.Token)
	assert.WithinDuration(t, exp, result.ExpiresAt, 0)

	// Issuer, Subject, and Audience come off the same decode as ExpiresAt, so
	// callers never re-parse the token.
	assert.Equal(t, "https://gitlab.example.com", result.Issuer)
	assert.Equal(t, "gid://gitlab/User/1", result.Subject)
	assert.Equal(t, "gitlab-artifact-registry,gitlab-iam-data-access", result.Audience)
}

func TestExchangeDefaultToken_OmitsExpiresIn(t *testing.T) {
	exp := time.Now().Add(5 * time.Minute).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		Audience:  jwt.ClaimStrings{"gitlab-artifact-registry"},
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody map[string]any
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	client := newTestClient(t, srv.URL)

	result, err := client.ExchangeDefaultToken(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.EqualValues(t, 1, count.Load())
	_, hasExpiresIn := gotBody["expires_in"]
	assert.False(t, hasExpiresIn, "ExchangeDefaultToken must omit expires_in so the server applies its own default")
	assert.WithinDuration(t, exp, result.ExpiresAt, 0)
}

func TestExchangeToken_DurationOutOfRange(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
	}{
		{
			name:     "below MinDuration",
			duration: 999 * time.Millisecond,
		},
		{
			name:     "above MaxDuration",
			duration: 13 * time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request to fake server: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			})

			client := newTestClient(t, srv.URL)

			result, err := client.ExchangeToken(t.Context(), tc.duration)
			require.Error(t, err)
			assert.Nil(t, result)
			assert.EqualValues(t, 0, count.Load(), "client must reject an invalid duration before making an HTTP call")
		})
	}
}

func TestValidateDuration(t *testing.T) {
	tests := []struct {
		name    string
		d       time.Duration
		wantErr bool
	}{
		{name: "exactly MinDuration is valid", d: MinDuration, wantErr: false},
		{name: "exactly MaxDuration is valid", d: MaxDuration, wantErr: false},
		{name: "one nanosecond below MinDuration is invalid", d: MinDuration - time.Nanosecond, wantErr: true},
		{name: "one nanosecond above MaxDuration is invalid", d: MaxDuration + time.Nanosecond, wantErr: true},
		{name: "zero duration is invalid", d: 0, wantErr: true},
		{name: "negative duration is invalid", d: -1 * time.Hour, wantErr: true},
		{name: "well above MaxDuration is invalid", d: 13 * time.Hour, wantErr: true},
		{name: "within range is valid", d: time.Hour, wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDuration(tc.d)
			if tc.wantErr {
				require.Error(t, err)
				// The error names the bounds, so a CLI error message needs no
				// extra context.
				assert.Contains(t, err.Error(), MinDuration.String())
				assert.Contains(t, err.Error(), MaxDuration.String())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
