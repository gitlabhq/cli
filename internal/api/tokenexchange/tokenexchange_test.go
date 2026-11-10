//go:build !integration

package tokenexchange

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

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/testing/tokenexchangetest"
)

const testAudience = "gitlab-example-service"

type wireRequest = tokenexchangetest.WireRequest

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return tokenexchangetest.NewTestServer(t, handler)
}

func newTestClient(t *testing.T, baseURL string) *gitlab.Client {
	t.Helper()
	return tokenexchangetest.NewTestClient(t, baseURL)
}

func makeJWT(t *testing.T, claims jwt.RegisteredClaims) string {
	t.Helper()
	return tokenexchangetest.MakeJWT(t, claims)
}

func TestExchange_Success(t *testing.T) {
	exp := time.Now().Add(time.Minute).Truncate(time.Second)
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		Audience:  jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody wireRequest
	srv, count := tokenexchangetest.NewTokenExchangeServer(t, wantToken, &gotBody)

	result, err := Exchange(t.Context(), newTestClient(t, srv.URL), testAudience, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.EqualValues(t, 1, count.Load())
	assert.Equal(t, wireRequest{Audience: testAudience, ExpiresIn: 60}, gotBody)
	assert.Equal(t, wantToken, result.Token)
	assert.WithinDuration(t, exp, result.ExpiresAt, 0)
	assert.Equal(t, "https://gitlab.example.com", result.Issuer)
	assert.Equal(t, "gid://gitlab/User/1", result.Subject)
	assert.Equal(t, testAudience, result.Audience)
}

func TestExchange_ZeroDurationOmitsExpiresIn(t *testing.T) {
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	})

	var gotBody map[string]any
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	_, err := Exchange(t.Context(), newTestClient(t, srv.URL), testAudience, 0)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"audience": testAudience}, gotBody)
}

func TestExchange_RoundsSubSecondDuration(t *testing.T) {
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
			gl := newTestClient(t, srv.URL)

			_, err := Exchange(t.Context(), gl, testAudience, tc.duration)
			require.NoError(t, err)
			assert.Equal(t, tc.wantExpireIn, gotBody.ExpiresIn)
		})
	}
}

func TestExchange_ServerBadRequest(t *testing.T) {
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"expires_in does not have a valid value"}`))
	})

	gl := newTestClient(t, srv.URL)

	result, err := Exchange(t.Context(), gl, testAudience, 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "expires_in does not have a valid value")
	assert.EqualValues(t, 1, count.Load())
}

func TestExchange_NotFound(t *testing.T) {
	// An instance that does not serve token exchange responds 404: a
	// Community Edition instance, a release before 19.1, or 19.1 to 19.3
	// with the gate_token_exchange_endpoint feature flag disabled.
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	gl := newTestClient(t, srv.URL)

	result, err := Exchange(t.Context(), gl, testAudience, 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "token exchange is not available on this instance (requires GitLab EE 19.4 or later)")
	assert.EqualValues(t, 1, count.Load())
}

func TestExchange_EmptyToken(t *testing.T) {
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	})

	gl := newTestClient(t, srv.URL)

	result, err := Exchange(t.Context(), gl, testAudience, 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "empty token")
	assert.EqualValues(t, 1, count.Load())
}

func TestExchange_MissingIssuerClaim(t *testing.T) {
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
	})

	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	gl := newTestClient(t, srv.URL)

	result, err := Exchange(t.Context(), gl, testAudience, 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no issuer claim")
}

func TestExchange_MissingSubjectClaim(t *testing.T) {
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
	})

	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	gl := newTestClient(t, srv.URL)

	result, err := Exchange(t.Context(), gl, testAudience, 15*time.Minute)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no subject claim")
}

func TestExchange_MalformedToken(t *testing.T) {
	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"not-a-jwt"}`))
	})

	gl := newTestClient(t, srv.URL)

	require.NotPanics(t, func() {
		result, err := Exchange(t.Context(), gl, testAudience, 15*time.Minute)
		require.Error(t, err)
		assert.Nil(t, result)
	})
	assert.EqualValues(t, 1, count.Load())
}

func TestExchange_RejectsTokenCarryingLineBreaks(t *testing.T) {
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

			result, err := Exchange(t.Context(), newTestClient(t, srv.URL), testAudience, 15*time.Minute)
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), "not in JWS compact form")
			assert.EqualValues(t, 1, count.Load())
		})
	}
}

func TestExchange_AcceptsEmptySignature(t *testing.T) {
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

	result, err := Exchange(t.Context(), newTestClient(t, srv.URL), testAudience, 15*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, unsigned, result.Token)
}

func mustJSON(t *testing.T, token string) []byte {
	t.Helper()

	body, err := json.Marshal(struct {
		Token string `json:"token"`
	}{Token: token})
	require.NoError(t, err)

	return body
}

func TestExchange_MissingExpiryClaim(t *testing.T) {
	// ExpiresAt is a *jwt.NumericDate, so a token with no exp claim decodes fine
	// and leaves it nil. Exchange must error rather than dereference it.
	wantToken := makeJWT(t, jwt.RegisteredClaims{
		Issuer:  "https://gitlab.example.com",
		Subject: "gid://gitlab/User/1",
	})

	srv, count := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})

	gl := newTestClient(t, srv.URL)

	require.NotPanics(t, func() {
		result, err := Exchange(t.Context(), gl, testAudience, 15*time.Minute)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "no expiry claim")
	})
	assert.EqualValues(t, 1, count.Load())
}

func TestResult_RedactsToken(t *testing.T) {
	result := Result{
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
