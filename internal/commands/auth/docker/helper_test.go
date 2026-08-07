//go:build !integration

package docker

import (
	"errors"
	"testing"
	"time"

	"github.com/docker/docker-credential-helpers/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
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
					expectErr:   "glab token for this registryURL (hostname) is empty",
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
					expectErr:   "glab token for this registryURL (hostname) is empty",
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

// TestParseDomains covers that a trailing, leading, or doubled comma in
// container_registry_domains must not produce a blank domain: an empty
// string reaching dockercredhelper.Register would write "": "glab" into
// the user's config.json.
func TestParseDomains(t *testing.T) {
	tests := map[string]struct {
		domains string
		want    []string
	}{
		"empty":          {domains: "", want: nil},
		"single":         {domains: "registry.example.com", want: []string{"registry.example.com"}},
		"multiple":       {domains: "registry.example.com, registry.other.example.com", want: []string{"registry.example.com", "registry.other.example.com"}},
		"trailing comma": {domains: "registry.example.com,", want: []string{"registry.example.com"}},
		"leading comma":  {domains: ",registry.example.com", want: []string{"registry.example.com"}},
		"doubled comma":  {domains: "registry.example.com,,registry.other.example.com", want: []string{"registry.example.com", "registry.other.example.com"}},
		"all whitespace": {domains: "   ", want: nil},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseDomains(tt.domains))
		})
	}
}
