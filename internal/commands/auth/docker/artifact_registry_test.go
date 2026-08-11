//go:build !integration

package docker

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/artifactregistrytest"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestHelper_findArtifactRegistryHostname(t *testing.T) {
	t.Run("resolves the matching host", func(t *testing.T) {
		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    artifact_registry_domains: registry.gitlab.example.com
`)
		helper := Helper{cfg: cfg}

		hostname, err := helper.findArtifactRegistryHostname("registry.gitlab.example.com")
		require.NoError(t, err)
		assert.Equal(t, "gitlab.example.com", hostname)
	})

	t.Run("picks the right host among several", func(t *testing.T) {
		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    artifact_registry_domains: registry.gitlab.example.com
  gdk.example.com:
    artifact_registry_domains: registry.gdk.example.com
`)
		helper := Helper{cfg: cfg}

		hostname, err := helper.findArtifactRegistryHostname("registry.gdk.example.com")
		require.NoError(t, err)
		assert.Equal(t, "gdk.example.com", hostname)
	})

	t.Run("returns the fall-through sentinel when unconfigured", func(t *testing.T) {
		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
`)
		helper := Helper{cfg: cfg}

		_, err := helper.findArtifactRegistryHostname("registry.gitlab.example.com")
		require.ErrorIs(t, err, errNotArtifactRegistry)
	})

	t.Run("does not match on container_registry_domains", func(t *testing.T) {
		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    container_registry_domains: registry.gitlab.example.com
`)
		helper := Helper{cfg: cfg}

		_, err := helper.findArtifactRegistryHostname("registry.gitlab.example.com")
		require.ErrorIs(t, err, errNotArtifactRegistry)
	})

	t.Run("propagates a real read failure", func(t *testing.T) {
		cfg := failingConfig{
			Config: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
`),
			failKey: "artifact_registry_domains",
			err:     errors.New("keyring is locked"),
		}
		helper := Helper{cfg: cfg}

		_, err := helper.findArtifactRegistryHostname("registry.gitlab.example.com")
		require.ErrorContains(t, err, "keyring is locked")
		require.NotErrorIs(t, err, errNotArtifactRegistry)
	})

	t.Run("a broken host does not hide a matching host", func(t *testing.T) {
		cfg := failingConfig{
			Config: config.NewFromString(`
---
hosts:
  broken.example.com:
    token: token1
  gdk.example.com:
    artifact_registry_domains: registry.gdk.example.com
`),
			failHost: "broken.example.com",
			failKey:  "artifact_registry_domains",
			err:      errors.New("keyring is locked"),
		}
		ios, _, _, _ := cmdtest.TestIOStreams()
		helper := Helper{cfg: cfg, io: ios}

		hostname, err := helper.findArtifactRegistryHostname("registry.gdk.example.com")
		require.NoError(t, err)
		assert.Equal(t, "gdk.example.com", hostname)
	})
}

func TestHelper_getArtifactRegistryToken(t *testing.T) {
	t.Run("no host configured", func(t *testing.T) {
		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
`)
		helper := Helper{cfg: cfg}

		_, _, err := helper.getArtifactRegistryToken(t.Context(), "registry.gitlab.example.com")
		require.ErrorIs(t, err, errNotArtifactRegistry)
	})

	t.Run("a server error is not mistaken for the fall-through sentinel", func(t *testing.T) {
		srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"artifact registry is disabled for this project"}`))
		})
		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
    artifact_registry_domains: registry.gitlab.example.com
    api_host: ` + apiHost(t, srv) + `
    api_protocol: http
`)
		helper := Helper{cfg: cfg}

		_, _, err := helper.getArtifactRegistryToken(t.Context(), "registry.gitlab.example.com")
		require.Error(t, err)
		require.NotErrorIs(t, err, errNotArtifactRegistry)
		assert.Contains(t, err.Error(), "artifact registry is disabled for this project")
	})

	t.Run("success returns the token convention Docker expects", func(t *testing.T) {
		wantToken := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
			Issuer:    "https://gitlab.example.com",
			Subject:   "gid://gitlab/User/1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		})
		var gotBody artifactregistrytest.WireRequest
		srv, count := artifactregistrytest.NewTokenExchangeServer(t, wantToken, &gotBody)
		cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
    artifact_registry_domains: registry.gitlab.example.com
    api_host: ` + apiHost(t, srv) + `
    api_protocol: http
`)
		helper := Helper{cfg: cfg}

		gotUser, gotToken, err := helper.getArtifactRegistryToken(t.Context(), "registry.gitlab.example.com")
		require.NoError(t, err)
		assert.Equal(t, "__token__", gotUser)
		assert.Equal(t, wantToken, gotToken)
		assert.EqualValues(t, 1, count.Load())
		// Pins artifactregistry.DockerHelperDuration's value at 1 hour (in
		// seconds, the wire format): without this, the request body is never
		// inspected, so a change to the duration this helper requests would go
		// unnoticed. Hardcoded rather than derived from the constant itself,
		// so the assertion cannot silently follow a future change to it.
		assert.Equal(t, int(time.Hour.Seconds()), gotBody.ExpiresIn)
	})
}

// TestHelper_getArtifactRegistryToken_IgnoresEnvironmentToken pins the same
// rule the container-registry path already relies on: a Docker credential
// helper runs as a subprocess that inherits the user's shell environment, so
// a stray GITLAB_TOKEN must not decide which identity mints the token.
func TestHelper_getArtifactRegistryToken_IgnoresEnvironmentToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "env-token")

	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "config-token", r.Header.Get("Private-Token"))

		wantToken := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
			Issuer:    "https://gitlab.example.com",
			Subject:   "gid://gitlab/User/1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"` + wantToken + `"}`))
	})
	cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: config-token
    artifact_registry_domains: registry.gitlab.example.com
    api_host: ` + apiHost(t, srv) + `
    api_protocol: http
`)
	helper := Helper{cfg: cfg}

	_, _, err := helper.getArtifactRegistryToken(t.Context(), "registry.gitlab.example.com")
	require.NoError(t, err)
}
