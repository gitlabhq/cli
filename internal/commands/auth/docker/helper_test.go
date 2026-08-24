//go:build !integration

package docker

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/docker/docker-credential-helpers/credentials"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/artifactregistrytest"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestHelper(t *testing.T) {
	// This avoids the oauth2 refresh from sending an http request.
	futureDate := time.Now().Add(24 * time.Hour).Format(time.RFC822)

	t.Run("Get", func(t *testing.T) {
		// This ensures that we don't pull the wrong user or token
		// if the config file search is incorrectly done with
		// env variable search space included.
		t.Setenv("USER", "wrong_user")
		t.Setenv("GITLAB_TOKEN", "wrong_token")

		t.Run("without error", func(t *testing.T) {
			tests := map[string]struct {
				cfg            config.Config
				registryURL    string
				expectUser     string
				expectPassword string
			}{
				"single host": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
`),
					registryURL:    "registry.gitlab.example.com",
					expectUser:     "user1",
					expectPassword: "token1",
				},
				"multi-host": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
  gdk.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user2
    token: token2
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gdk.example.com
`),
					registryURL:    "registry.gdk.example.com",
					expectUser:     "user2",
					expectPassword: "token2",
				},
				// A host authenticated with a personal access token has no
				// oauth2_expiry_date, so any attempt to refresh fails to parse it.
				"personal access token host": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.com:
    user: user1
    token: token1
    container_registry_domains: registry.gitlab.com
`),
					registryURL:    "registry.gitlab.com",
					expectUser:     "user1",
					expectPassword: "token1",
				},
			}

			for name, tt := range tests {
				t.Run(name, func(t *testing.T) {
					helper := Helper{cfg: tt.cfg}
					gotUser, gotPassword, err := helper.Get(tt.registryURL)
					require.NoError(t, err)
					assert.Equal(t, tt.expectUser, gotUser, "username does not match")
					assert.Equal(t, tt.expectPassword, gotPassword, "password does not match")
				})
			}
		})

		t.Run("with error", func(t *testing.T) {
			tests := map[string]struct {
				cfg         config.Config
				registryURL string
				expectErr   string
			}{
				"no associated hostname": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
`),
					registryURL: "gitlab.example.com",
					expectErr:   "no hostname associated with",
				},
				"empty username": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: ""
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
`),
					registryURL: "registry.gitlab.example.com",
					expectErr:   "glab user for this registryURL (hostname) is empty",
				},
				"empty token": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: ""
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
`),
					registryURL: "registry.gitlab.example.com",
					// With no per-host token and no refresh token, the
					// upfront oauth2 refresh (kept out of the environment by
					// searchEnvForIdentity=false) fails before the empty-token
					// check downstream is even reached.
					expectErr: "oauth2: token expired and refresh token is not set",
				},
				"no username": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
`),
					registryURL: "registry.gitlab.example.com",
					expectErr:   "glab user for this registryURL (hostname) is empty",
				},
				"no token": {
					cfg: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
`),
					registryURL: "registry.gitlab.example.com",
					// Same as "empty token": the upfront oauth2 refresh fails
					// first since there is no per-host token or refresh token.
					expectErr: "oauth2: token expired and refresh token is not set",
				},
			}

			for name, tt := range tests {
				t.Run(name, func(t *testing.T) {
					helper := Helper{cfg: tt.cfg}
					gotUser, gotPassword, err := helper.Get(tt.registryURL)
					require.ErrorContains(t, err, tt.expectErr)
					assert.Empty(t, gotUser, "username is not empty")
					assert.Empty(t, gotPassword, "password is not empty")
				})
			}
		})

		t.Run("GLAB_IS_OAUTH2 does not force the refresh onto a PAT host", func(t *testing.T) {
			t.Setenv("GLAB_IS_OAUTH2", "true")

			helper := Helper{cfg: config.NewFromString(`
---
hosts:
  gitlab.com:
    user: user1
    token: token1
    container_registry_domains: registry.gitlab.com
`)}

			gotUser, gotPassword, err := helper.Get("registry.gitlab.com")
			require.NoError(t, err)
			assert.Equal(t, "user1", gotUser)
			assert.Equal(t, "token1", gotPassword)
		})
	})

	// A read failure must name itself rather than arrive as "no hostname
	// associated with registryURL", which points at configuration that exists
	// but couldn't be read.
	t.Run("reports a config read failure instead of an unknown domain", func(t *testing.T) {
		cfg := failingConfig{
			Config: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
`),
			failKey: "container_registry_domains",
			err:     errors.New("keyring is locked"),
		}
		helper := Helper{cfg: cfg}

		_, _, err := helper.Get("registry.gitlab.example.com")
		require.ErrorContains(t, err, "keyring is locked")
		assert.NotContains(t, err.Error(), "no hostname associated")
	})

	// The read error is collected, not returned on the spot, so one broken host
	// entry cannot stop an unrelated host from resolving the domain.
	t.Run("a broken host entry does not hide a matching host", func(t *testing.T) {
		cfg := failingConfig{
			Config: config.NewFromString(`
---
hosts:
  broken.example.com:
    token: token1
  gdk.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user2
    token: token2
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gdk.example.com
`),
			failHost: "broken.example.com",
			failKey:  "container_registry_domains",
			err:      errors.New("keyring is locked"),
		}
		ios, _, _, errOut := cmdtest.TestIOStreams()
		helper := Helper{cfg: cfg, io: ios}

		gotUser, gotPassword, err := helper.Get("registry.gdk.example.com")
		require.NoError(t, err)
		assert.Equal(t, "user2", gotUser)
		assert.Equal(t, "token2", gotPassword)
		// broken.example.com could have been the intended host for this
		// domain too; its config just couldn't be read, so the match on
		// gdk.example.com must not look unconditionally trustworthy.
		assert.Contains(t, errOut.String(), "keyring is locked")
	})

	t.Run("Add", func(t *testing.T) {
		var helper Helper
		err := helper.Add(&credentials.Credentials{})
		assert.ErrorContains(t, err, "glab auth docker-helper does not")
	})

	t.Run("Delete", func(t *testing.T) {
		var helper Helper
		err := helper.Delete("registry.gitlab.example.com")
		assert.ErrorContains(t, err, "glab auth docker-helper does not")
	})

	t.Run("List", func(t *testing.T) {
		var helper Helper
		got, err := helper.List()
		require.ErrorContains(t, err, "glab auth docker-helper does not")
		assert.Empty(t, got)
	})
}

// TestHelper_Get_Precedence pins the dual-listed-domain precedence rule: the
// artifact registry is tried first, the container registry is only a
// fallback, and a domain configured solely for artifact registry gets its
// real exchange error rather than "unknown domain".
func TestHelper_Get_Precedence(t *testing.T) {
	futureDate := time.Now().Add(24 * time.Hour).Format(time.RFC822)

	t.Run("dual-listed domain, artifact registry exchange succeeds", func(t *testing.T) {
		wantToken := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
			Issuer:    "https://gitlab.example.com",
			Subject:   "gid://gitlab/User/1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		})
		srv, count := artifactregistrytest.NewTokenExchangeServer(t, wantToken, &artifactregistrytest.WireRequest{})
		host := apiHost(t, srv)

		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
    artifact_registry_domains: registry.gitlab.example.com
    api_host: ` + host + `
    api_protocol: http
`)
		helper := Helper{cfg: cfg}

		gotUser, gotToken, err := helper.Get("registry.gitlab.example.com")
		require.NoError(t, err)
		assert.Equal(t, "__token__", gotUser)
		assert.Equal(t, wantToken, gotToken)
		assert.EqualValues(t, 1, count.Load())
	})

	t.Run("dual-listed domain, artifact registry exchange fails, container registry used", func(t *testing.T) {
		srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		host := apiHost(t, srv)

		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
    artifact_registry_domains: registry.gitlab.example.com
    api_host: ` + host + `
    api_protocol: http
`)
		ios, _, _, errOut := cmdtest.TestIOStreams()
		helper := Helper{cfg: cfg, io: ios}

		gotUser, gotToken, err := helper.Get("registry.gitlab.example.com")
		require.NoError(t, err)
		assert.Equal(t, "user1", gotUser)
		assert.Equal(t, "token1", gotToken)
		// The fall-through must be visible on stderr without DEBUG=1, since
		// Docker forwards the helper's stderr but not its debug logging.
		assert.Contains(t, errOut.String(), "artifact registry exchange failed")
	})

	t.Run("artifact-registry-only domain, exchange fails, hard error", func(t *testing.T) {
		srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"artifact registry is disabled for this project"}`))
		})
		host := apiHost(t, srv)

		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    artifact_registry_domains: registry.gitlab.example.com
    api_host: ` + host + `
    api_protocol: http
`)
		helper := Helper{cfg: cfg}

		_, _, err := helper.Get("registry.gitlab.example.com")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "artifact registry is disabled for this project")
		assert.NotContains(t, err.Error(), "no hostname associated")
	})

	// Dual-listed domain, both exchanges fail: the container-registry error
	// names a real failure (an empty stored token), not just an unassociated
	// domain, so it must not be swallowed by the artifact-registry error alone.
	t.Run("dual-listed domain, both exchanges fail, container registry error is not swallowed", func(t *testing.T) {
		srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"artifact registry is disabled for this project"}`))
		})
		host := apiHost(t, srv)

		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: ""
    token: token1
    oauth2_expiry_date: ` + futureDate + `
    container_registry_domains: registry.gitlab.example.com
    artifact_registry_domains: registry.gitlab.example.com
    api_host: ` + host + `
    api_protocol: http
`)
		helper := Helper{cfg: cfg}

		_, _, err := helper.Get("registry.gitlab.example.com")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "artifact registry is disabled for this project")
		assert.Contains(t, err.Error(), "glab user for this registryURL (hostname) is empty")
	})

	t.Run("a config read failure surfaces as itself, not as an unknown domain", func(t *testing.T) {
		cfg := failingConfig{
			Config: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    is_oauth2: "true"
    client_id: abc
    user: user1
    token: token1
    oauth2_expiry_date: ` + futureDate + `
`),
			failKey: "artifact_registry_domains",
			err:     errors.New("keyring is locked"),
		}
		helper := Helper{cfg: cfg}

		_, _, err := helper.Get("registry.gitlab.example.com")
		require.ErrorContains(t, err, "keyring is locked")
		assert.NotContains(t, err.Error(), "no hostname associated")
	})
}

// apiHost returns srv's host:port, since the api_host config key takes a
// bare host:port rather than a full URL.
func apiHost(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u.Host
}
