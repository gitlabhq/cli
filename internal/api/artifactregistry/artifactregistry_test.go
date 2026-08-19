//go:build !integration

package artifactregistry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/artifactregistrytest"
)

// wireRequest mirrors the JSON body artifactregistry is expected to send to
// POST /api/v4/token_exchange.
type wireRequest = artifactregistrytest.WireRequest

// newTestServer starts a fake token_exchange endpoint and returns it along
// with a counter of how many requests it received, so tests can assert that
// invalid input never reaches the network.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return artifactregistrytest.NewTestServer(t, handler)
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClient(artifactregistrytest.NewTestClient(t, baseURL))
}

// makeJWT mints a JWT carrying claims. artifactregistry never verifies token
// signatures (it trusts the TLS connection to the GitLab host that issued
// the token), so any signing key produces a token decodeUnverifiedClaims
// accepts.
func makeJWT(t *testing.T, claims jwt.RegisteredClaims) string {
	t.Helper()
	return artifactregistrytest.MakeJWT(t, claims)
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

// TestExchangeToken_RoundsSubSecondDuration guards against reintroducing
// truncation of the sub-second remainder: harmless when MinDuration was 15
// minutes, but now that it is 1 second, truncating toward zero can discard
// nearly a full second of a short, precisely-chosen request.
func TestExchangeToken_RoundsSubSecondDuration(t *testing.T) {
	tests := []struct {
		duration     time.Duration
		wantExpireIn int
	}{
		{duration: 1500 * time.Millisecond, wantExpireIn: 2},
		{duration: 1100 * time.Millisecond, wantExpireIn: 1},
	}

	for _, tc := range tests {
		t.Run(tc.duration.String(), func(t *testing.T) {
			wantToken := makeJWT(t, jwt.RegisteredClaims{
				Issuer:    "https://gitlab.example.com",
				Subject:   "gid://gitlab/User/1",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(tc.duration)),
			})

			var gotBody wireRequest
			srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
			})
			client := newTestClient(t, srv.URL)

			_, err := client.ExchangeToken(t.Context(), tc.duration)
			require.NoError(t, err)
			assert.Equal(t, tc.wantExpireIn, gotBody.ExpiresIn)
		})
	}
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

func TestExchangeToken_ServerBadRequest(t *testing.T) {
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"expires_in does not have a valid value"}`))
	})

	client := newTestClient(t, srv.URL)

	result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "expires_in does not have a valid value")
	assert.EqualValues(t, 1, count.Load())
}

func TestExchangeToken_NotFound(t *testing.T) {
	// gate_token_exchange_endpoint disabled on the instance means the
	// endpoint itself does not exist, so the server 404s.
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	client := newTestClient(t, srv.URL)

	result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "token exchange is not enabled on this instance")
	assert.EqualValues(t, 1, count.Load())
}

func TestExchangeToken_EmptyToken(t *testing.T) {
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	})

	client := newTestClient(t, srv.URL)

	result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "empty token")
	assert.EqualValues(t, 1, count.Load())
}

func TestExchangeToken_MissingIssuerClaim(t *testing.T) {
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
	})

	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	client := newTestClient(t, srv.URL)

	result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no issuer claim")
}

func TestExchangeToken_MissingSubjectClaim(t *testing.T) {
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
	})

	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	client := newTestClient(t, srv.URL)

	result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no subject claim")
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

func TestExchangeToken_MalformedToken(t *testing.T) {
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"not-a-jwt"}`))
	})

	client := newTestClient(t, srv.URL)

	require.NotPanics(t, func() {
		result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
		require.Error(t, err)
		assert.Nil(t, result)
	})
	assert.EqualValues(t, 1, count.Load())
}

// TestExchangeToken_RejectsTokenCarryingLineBreaks pins the guard that keeps
// line breaks out of a token every caller writes verbatim into a credential
// file. decodeUnverifiedClaims does not catch these on its own: Go's base64
// decoder treats "\r" and "\n" as insignificant whitespace, so each of these
// tokens decodes with valid exp/iss/sub claims and would reach the writer.
func TestExchangeToken_RejectsTokenCarryingLineBreaks(t *testing.T) {
	valid := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour).Truncate(time.Second)),
	})
	segments := strings.Split(valid, ".")
	require.Len(t, segments, 3)

	tests := map[string]string{
		"newline inside the signature": segments[0] + "." + segments[1] + "." + segments[2][:3] + "\n" + segments[2][3:],
		"newline inside the payload":   segments[0] + "." + segments[1] + "\n." + segments[2],
		"trailing newline":             valid + "\n",
		"leading newline":              "\n" + valid,
		"carriage return":              valid + "\r",
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			// Proves the guard is load-bearing rather than a restatement of
			// what the claims decode already rejects.
			claims, decodeErr := decodeUnverifiedClaims(token)
			require.NoError(t, decodeErr, "precondition: this token decodes, so only the shape guard can reject it")
			require.Equal(t, "https://gitlab.example.com", claims.Issuer)

			srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(mustJSON(t, token))
			})

			result, err := newTestClient(t, srv.URL).ExchangeToken(t.Context(), 15*time.Minute)
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), "not in JWS compact form")
			assert.EqualValues(t, 1, count.Load())
		})
	}
}

// TestExchangeToken_AcceptsEmptySignature keeps the shape guard from
// over-rejecting: an alg=none token has an empty third segment and is still
// well-formed, so the guard must not require a signature to be present.
func TestExchangeToken_AcceptsEmptySignature(t *testing.T) {
	valid := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour).Truncate(time.Second)),
	})
	segments := strings.Split(valid, ".")
	unsigned := segments[0] + "." + segments[1] + "."

	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mustJSON(t, unsigned))
	})

	result, err := newTestClient(t, srv.URL).ExchangeToken(t.Context(), 15*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, unsigned, result.Token)
}

// mustJSON renders {"token": <token>} with the token JSON-escaped, so a test
// fixture carrying a newline reaches the client as that newline rather than as
// an invalid JSON body the transport rejects first.
func mustJSON(t *testing.T, token string) []byte {
	t.Helper()

	body, err := json.Marshal(struct {
		Token string `json:"token"`
	}{Token: token})
	require.NoError(t, err)

	return body
}

func TestExchangeToken_MissingExpiryClaim(t *testing.T) {
	// ExpiresAt is a *jwt.NumericDate, so a token with no exp claim decodes fine
	// and leaves it nil. ExchangeToken must error rather than dereference it.
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:  "https://gitlab.example.com",
		Subject: "gid://gitlab/User/1",
	})

	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	client := newTestClient(t, srv.URL)

	require.NotPanics(t, func() {
		result, err := client.ExchangeToken(t.Context(), 15*time.Minute)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "no expiry claim")
	})
	assert.EqualValues(t, 1, count.Load())
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

func TestExchangeResult_RedactsToken(t *testing.T) {
	result := ExchangeResult{
		Token:     "super-secret-bearer-token",
		ExpiresAt: time.Now(),
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		Audience:  "gitlab-artifact-registry",
	}

	t.Run("String", func(t *testing.T) {
		s := fmt.Sprintf("%+v", result)
		assert.NotContains(t, s, result.Token)
	})

	t.Run("MarshalJSON", func(t *testing.T) {
		b, err := json.Marshal(result)
		require.NoError(t, err)
		assert.NotContains(t, string(b), result.Token)
	})
}

func TestDecodeUnverifiedClaims(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		exp := time.Now().Add(30 * time.Minute).Truncate(time.Second)
		token := makeJWT(t, jwt.RegisteredClaims{
			Issuer:    "https://gitlab.example.com",
			Subject:   "gid://gitlab/User/42",
			ExpiresAt: jwt.NewNumericDate(exp),
		})

		claims, err := decodeUnverifiedClaims(token)
		require.NoError(t, err)
		require.NotNil(t, claims)

		assert.Equal(t, "https://gitlab.example.com", claims.Issuer)
		assert.Equal(t, "gid://gitlab/User/42", claims.Subject)
		require.NotNil(t, claims.ExpiresAt)
		assert.WithinDuration(t, exp, claims.ExpiresAt.Time, 0)
	})

	t.Run("malformed token", func(t *testing.T) {
		claims, err := decodeUnverifiedClaims("not-a-jwt")
		require.Error(t, err)
		assert.Nil(t, claims)
	})
}
