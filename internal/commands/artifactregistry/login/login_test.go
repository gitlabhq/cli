//go:build !integration

package login

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/api/artifactregistry"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/artifactregistrytest"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// apiHost returns srv's host:port, since the api_host config key takes a bare
// host:port rather than a full URL.
func apiHost(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u.Host
}

// storedToken is the credential newServerConfig puts in the configuration file,
// named so a test can tell it apart from one set in the environment.
const storedToken = "config-token"

// newServerConfig builds a config that routes each of hostnames' API traffic to
// srv and gives each a stored token.
//
// The token goes in the configuration file rather than GITLAB_TOKEN because the
// --docker path builds its client with api.WithoutTokenFromEnvironment, the same
// way the Docker credential helper builds its own, so an environment token would
// be ignored here exactly as it is at `docker pull` time.
func newServerConfig(t *testing.T, srv *httptest.Server, hostnames ...string) config.Config {
	t.Helper()

	var yaml strings.Builder
	yaml.WriteString("---\nhosts:\n")
	for _, hostname := range hostnames {
		yaml.WriteString("  " + hostname + ":\n" +
			"    token: " + storedToken + "\n" +
			"    api_host: " + apiHost(t, srv) + "\n" +
			"    api_protocol: http\n")
	}

	return config.NewFromString(yaml.String())
}

// clearAmbientTokens empties every environment variable the token and job_token
// config keys resolve through, so a developer's shell or a CI job cannot decide
// whether a test's host counts as having a credential. GLAB_ENABLE_CI_AUTOLOGIN
// is in the list because it is what maps job_token onto CI_JOB_TOKEN.
func clearAmbientTokens(t *testing.T) {
	t.Helper()

	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "JOB_TOKEN", "GLAB_ENABLE_CI_AUTOLOGIN"} {
		t.Setenv(name, "")
	}
}

// newTestExec wires NewCmd to cfg, which is where the --docker path finds
// everything it needs: the API endpoint and credential for the verification
// exchange, and the artifact_registry_domains it reads and writes.
func newTestExec(t *testing.T, cfg config.Config) cmdtest.CmdExecFunc {
	t.Helper()

	return cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithConfig(cfg))
}

// newMavenTestExec wires NewCmd to an api.Client pointed at srv. The --maven
// path exchanges through options.apiClient, which is f.ApiClient, so unlike
// newTestExec it needs no config: nothing on this path reads one.
func newMavenTestExec(t *testing.T, srv *httptest.Server) cmdtest.CmdExecFunc {
	t.Helper()

	return artifactregistrytest.NewTestExec(t, srv, NewCmd)
}

// wireRequest mirrors the JSON body `glab artifact-registry login` sends to
// POST /api/v4/token_exchange for the non-docker writers.
type wireRequest = artifactregistrytest.WireRequest

// tokenServer starts a fake token_exchange endpoint that always returns
// wantToken, decoding each request body into gotBody.
func tokenServer(t *testing.T, wantToken string, gotBody *wireRequest) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	return artifactregistrytest.NewTokenExchangeServer(t, wantToken, gotBody)
}

// supportedOS stands in for dockercredhelper.Supported in tests that build
// options directly, so they exercise validate on the supported-platform path
// whatever the test host is.
func supportedOS() error { return nil }

// dockerTokenServer starts a token_exchange endpoint that succeeds, for the
// --docker path's pre-write verification exchange.
func dockerTokenServer(t *testing.T) *httptest.Server {
	t.Helper()

	token := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour).Truncate(time.Second)),
	})

	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"` + token + `"}`))
	})
	return srv
}

func TestLogin_NoToolFlag(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags")
}

func TestLogin_TwoToolFlags(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--docker --maven --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestLogin_BothToolFlagsFalseLeavesNoneSelected(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	// MarkFlagsOneRequired only checks that a flag in the group was passed,
	// so --docker=false alone satisfies it and reaches validate() with
	// neither tool actually selected.
	_, err := exec("--docker=false --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --docker and --maven: both false leaves no tool selected")
}

func TestLogin_MissingRegistry(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--docker")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required flag")
}

func TestLogin_RegistryMustBeBareHostname(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--docker --registry https://has-a-scheme.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bare hostname")
}

func TestLogin_RegistryMustNotContainComma(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--docker --registry registry.example.com,other.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comma")
}

func TestLogin_RegistryMustNotBeEmpty(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	// cobra's MarkFlagRequired only checks that the flag was passed, so an
	// explicitly empty value reaches validate().
	_, err := exec(`--docker --registry ""`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --registry: must not be empty")
}

// TestLogin_RegistryMustNotContainControlCharacters exercises validate()
// directly: shlex-parsing a CLI string can't produce an embedded literal
// newline in --registry, so this goes through options.validate() rather
// than exec().
func TestLogin_RegistryMustNotContainControlCharacters(t *testing.T) {
	for name, o := range map[string]*options{
		"maven":  {registry: "https://ar.example.com/\nEvil=1", maven: true},
		"docker": {registry: "ar.example.com\nEvil=1", docker: true, supportedOS: supportedOS},
	} {
		t.Run(name, func(t *testing.T) {
			err := o.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "control characters")
		})
	}
}

func TestLogin_RegistryAliasMustBeSafeCharacters(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec(`--maven --registry https://ar.example.com --registry-alias "bad<id>"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--registry-alias")
}

func TestLogin_RegistryAliasIgnoredOutsideMavenWarning(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv := dockerTokenServer(t)
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	out, err := exec("--docker --registry registry.example.com --registry-alias my-alias")
	require.NoError(t, err)
	assert.Contains(t, out.ErrBuf.String(), "--registry-alias is ignored")
}

func TestLogin_Maven_DurationOutOfRange(t *testing.T) {
	srv, count := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to fake server: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	})
	exec := newMavenTestExec(t, srv)

	_, err := exec("--maven --registry https://ar.example.com --duration 13h")
	require.Error(t, err)
	assert.EqualValues(t, 0, count.Load(), "an out-of-range duration must be rejected before making an HTTP call")
}

func TestLogin_Maven_DispatchesToWriterAfterExchangingToken(t *testing.T) {
	setHome(t, t.TempDir())

	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	wantToken := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(exp),
	})

	var gotBody wireRequest
	srv, count := tokenServer(t, wantToken, &gotBody)
	exec := newMavenTestExec(t, srv)

	_, err := exec("--maven --registry https://ar.example.com")
	require.NoError(t, err)
	assert.EqualValues(t, 1, count.Load())
}

// TestLogin_Maven_ForwardsDurationToExchange pins the --duration value the
// --maven path puts on the wire, in both directions: an explicit value must
// reach token_exchange instead of the flag's default, and an omitted flag must
// send DefaultDuration rather than whatever a previous run asked for.
func TestLogin_Maven_ForwardsDurationToExchange(t *testing.T) {
	tests := []struct {
		name          string
		args          string
		wantExpiresIn time.Duration
	}{
		{
			name:          "explicit duration is forwarded",
			args:          "--maven --registry https://ar.example.com --duration 42m",
			wantExpiresIn: 42 * time.Minute,
		},
		{
			name:          "omitted duration sends the default",
			args:          "--maven --registry https://ar.example.com",
			wantExpiresIn: artifactregistry.DefaultDuration,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, t.TempDir())

			wantToken := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
				Issuer:    "https://gitlab.example.com",
				Subject:   "gid://gitlab/User/1",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(tc.wantExpiresIn).Truncate(time.Second)),
			})

			var gotBody wireRequest
			srv, count := tokenServer(t, wantToken, &gotBody)
			exec := newMavenTestExec(t, srv)

			_, err := exec(tc.args)
			require.NoError(t, err)

			assert.EqualValues(t, 1, count.Load())
			assert.Equal(t, wireRequest{
				Audience:  "gitlab-artifact-registry",
				ExpiresIn: int(tc.wantExpiresIn.Seconds()),
			}, gotBody)
		})
	}
}

// TestLogin_Maven_DefaultRegistryAliasIsDerivedFromRegistry pins that a
// --maven login with no --registry-alias writes a <server> block keyed by an
// alias derived from --registry, not a literal or empty id.
func TestLogin_Maven_DefaultRegistryAliasIsDerivedFromRegistry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	wantToken := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour).Truncate(time.Second)),
	})
	var gotBody wireRequest
	srv, _ := tokenServer(t, wantToken, &gotBody)
	exec := newMavenTestExec(t, srv)

	_, err := exec("--maven --registry https://ar.example.com")
	require.NoError(t, err)

	content, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	assert.Contains(t, string(content), "<id>artifact-registry-ar-example-com</id>")
}

// TestLogin_RegistryMustNotContainWhitespace covers the value that would
// otherwise be written verbatim into artifact_registry_domains and read back
// trimmed by config.ParseDomains: the credHelpers key Docker looks up would
// never match, and each login would append another entry to the domain list.
func TestLogin_RegistryMustNotContainWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		registry string
	}{
		{name: "leading space", registry: " registry.example.com"},
		{name: "trailing space", registry: "registry.example.com "},
		{name: "inner tab", registry: "registry.example.com\tother.example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

			_, err := exec(`--docker --registry "` + tc.registry + `"`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid --registry: must not contain whitespace or control characters")
		})
	}
}

func TestLogin_InvalidHostname(t *testing.T) {
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
			exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

			_, err := exec("--docker --registry registry.example.com --hostname " + tc.hostname)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "error parsing --hostname")
		})
	}
}

// TestLogin_HostnameMustNotContainWhitespace covers what
// glinstance.HostnameValidator lets through: it rejects only "/" and ":", so
// without this check a hostname with a space reaches glinstance.APIEndpoint and
// fails with a net/url parse error instead of a FlagError naming the flag.
func TestLogin_HostnameMustNotContainWhitespace(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec(`--docker --registry registry.example.com --hostname "gitlab example.com"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --hostname: must not contain whitespace or control characters")
}

func TestLogin_Docker_ExchangeFailureWritesNothing(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	// glab is on PATH here on purpose: with it missing, the shim assertion
	// below would pass because Install could never have run, rather than
	// because the failed exchange stopped it.
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"artifact registry is disabled for this project"}`))
	})
	cfg := newServerConfig(t, srv, glinstance.DefaultHostname)
	exec := newTestExec(t, cfg)

	_, err := exec("--docker --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact registry is disabled for this project")

	domains, getErr := cfg.Get(glinstance.DefaultHostname, "artifact_registry_domains")
	require.NoError(t, getErr)
	assert.Empty(t, domains, "a failed exchange must leave no config behind")

	_, statErr := os.Stat(filepath.Join(binDir, "docker-credential-glab"))
	assert.True(t, os.IsNotExist(statErr), "a failed exchange must leave no shim behind")

	_, statErr = os.Stat(filepath.Join(home, ".docker", "config.json"))
	assert.True(t, os.IsNotExist(statErr), "a failed exchange must leave no credHelpers entry behind")
}

// TestLogin_Docker_UnsupportedOSFailsBeforeTheExchange pins that an
// unsupported platform is rejected in validate, not by
// dockercredhelper.Install after run has already exchanged a token. A user on
// Windows must not mint a live credential to be told Docker can never use it.
func TestLogin_Docker_UnsupportedOSFailsBeforeTheExchange(t *testing.T) {
	o := &options{
		supportedOS: func() error { return errors.New(`operating system "windows" is not supported`) },
		registry:    "registry.example.com",
		docker:      true,
	}

	err := o.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not supported")
}

func TestLogin_RegistryMustNotContainPath(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--docker --registry registry.example.com/some/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain a path")
}

// TestLogin_Docker_VerificationTokenUsesMinDuration pins the lifetime of the
// token this command exchanges and throws away. Nothing sends it, so it asks
// for the shortest lifetime the server issues rather than the server default.
func TestLogin_Docker_VerificationTokenUsesMinDuration(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	token := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour).Truncate(time.Second)),
	})

	var gotBody struct {
		ExpiresIn *int `json:"expires_in"`
	}
	var decodeErr error
	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Recorded rather than asserted: a failed assertion inside a handler
		// runs on the server's goroutine, where it cannot fail the test.
		decodeErr = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"` + token + `"}`))
	})
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	_, err := exec("--docker --registry registry.example.com")
	require.NoError(t, err)

	require.NoError(t, decodeErr)
	require.NotNil(t, gotBody.ExpiresIn, "expires_in must be sent, so the server does not apply its own default")
	assert.Equal(t, int(artifactregistry.MinDuration.Seconds()), *gotBody.ExpiresIn)
}

// TestLogin_Docker_VerificationUsesTheStoredCredential pins that the
// verification exchange authenticates as the identity the Docker credential
// helper will use. The helper ignores GITLAB_TOKEN, so verifying with it would
// check an identity Docker never uses: a valid GITLAB_TOKEN over a dead stored
// token would report a successful login whose every `docker pull` then 401s.
func TestLogin_Docker_VerificationUsesTheStoredCredential(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())
	clearAmbientTokens(t)
	t.Setenv("GITLAB_TOKEN", "environment-token")

	token := artifactregistrytest.MakeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour).Truncate(time.Second)),
	})

	// Recorded rather than asserted: a failed assertion inside a handler runs on
	// the server's goroutine, where it cannot fail the test.
	var gotToken string
	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get(gitlab.AccessTokenHeaderName)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"` + token + `"}`))
	})
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	_, err := exec("--docker --registry registry.example.com")
	require.NoError(t, err)

	assert.Equal(t, storedToken, gotToken)
}

// TestLogin_Docker_VerificationFailsWhenOnlyTheEnvironmentHasAToken is the
// other half: with nothing stored for the host, the exchange goes out
// unauthenticated and the login fails, even though GITLAB_TOKEN would have made
// it succeed. That is the point, since `docker pull` has the same nothing to
// authenticate with, so the error has to name the environment token the user is
// about to blame for working everywhere else.
func TestLogin_Docker_VerificationFailsWhenOnlyTheEnvironmentHasAToken(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)
	clearAmbientTokens(t)
	t.Setenv("GITLAB_TOKEN", "environment-token")

	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(gitlab.AccessTokenHeaderName) == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"should not be reached"}`))
	})

	cfg := config.NewFromString("---\nhosts:\n  " + glinstance.DefaultHostname + ":\n" +
		"    user: someone\n" +
		"    api_host: " + apiHost(t, srv) + "\n" +
		"    api_protocol: http\n")
	exec := newTestExec(t, cfg)

	_, err := exec("--docker --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "GITLAB_TOKEN is set, but nothing is stored for "+glinstance.DefaultHostname)
	assert.Contains(t, err.Error(), "glab auth login --hostname "+glinstance.DefaultHostname)

	_, statErr := os.Stat(filepath.Join(home, ".docker", "config.json"))
	assert.True(t, os.IsNotExist(statErr), "a failed verification must leave no credHelpers entry behind")
}

// TestLogin_Docker_ExchangeFailureHintsAtTheMissingToken covers the same
// unauthenticated exchange with no environment token either. "401 Unauthorized"
// alone would not tell the user that `glab auth login` is what writes the file
// the Docker credential helper reads.
func TestLogin_Docker_ExchangeFailureHintsAtTheMissingToken(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())
	clearAmbientTokens(t)

	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	})

	cfg := config.NewFromString("---\nhosts:\n  " + glinstance.DefaultHostname + ":\n" +
		"    user: someone\n" +
		"    api_host: " + apiHost(t, srv) + "\n" +
		"    api_protocol: http\n")
	exec := newTestExec(t, cfg)

	_, err := exec("--docker --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No token is stored for "+glinstance.DefaultHostname)
	assert.Contains(t, err.Error(), "glab auth login --hostname "+glinstance.DefaultHostname)
}

// TestLogin_Docker_ExchangeFailureWithAJobTokenAddsNoHint covers the CI case: a
// host whose only credential is a job token is configured, since that is what
// the Docker credential helper authenticates with inside a CI job, so telling
// the user to run `glab auth login` would be wrong.
func TestLogin_Docker_ExchangeFailureWithAJobTokenAddsNoHint(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())
	clearAmbientTokens(t)

	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"403 Forbidden"}`))
	})

	cfg := config.NewFromString("---\nhosts:\n  " + glinstance.DefaultHostname + ":\n" +
		"    job_token: ci-job-token\n" +
		"    api_host: " + apiHost(t, srv) + "\n" +
		"    api_protocol: http\n")
	exec := newTestExec(t, cfg)

	_, err := exec("--docker --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403 Forbidden")
	assert.NotContains(t, err.Error(), "glab auth login")
	assert.NotContains(t, err.Error(), "No token is stored")
}

// TestLogin_Docker_ExchangeFailureWithAStoredTokenAddsNoHint keeps the hint
// honest: a host that does have a stored token failed the exchange for some
// other reason, and telling the user to log in again would send them the wrong
// way.
func TestLogin_Docker_ExchangeFailureWithAStoredTokenAddsNoHint(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv, _ := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"artifact registry is disabled for this project"}`))
	})
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	_, err := exec("--docker --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact registry is disabled for this project")
	assert.NotContains(t, err.Error(), "glab auth login")
}

func TestLogin_Docker_DefaultsHostnameWhenNotProvided(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv := dockerTokenServer(t)
	cfg := newServerConfig(t, srv, glinstance.DefaultHostname)
	exec := newTestExec(t, cfg)

	_, err := exec("--docker --registry registry.example.com")
	require.NoError(t, err)

	domains, err := cfg.Get(glinstance.DefaultHostname, "artifact_registry_domains")
	require.NoError(t, err)
	assert.Contains(t, domains, "registry.example.com")
}

func TestLogin_Docker_UsesExplicitHostnameWhenProvided(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv := dockerTokenServer(t)
	cfg := newServerConfig(t, srv, glinstance.DefaultHostname, "gitlab.other-instance.example.com")
	exec := newTestExec(t, cfg)

	_, err := exec("--docker --registry registry.example.com --hostname gitlab.other-instance.example.com")
	require.NoError(t, err)

	// The explicit hostname must be used as-is, not overridden by the
	// default-hostname fallback.
	domains, err := cfg.Get("gitlab.other-instance.example.com", "artifact_registry_domains")
	require.NoError(t, err)
	assert.Contains(t, domains, "registry.example.com")

	defaultDomains, err := cfg.Get(glinstance.DefaultHostname, "artifact_registry_domains")
	require.NoError(t, err)
	assert.NotContains(t, defaultDomains, "registry.example.com")
}

// TestLogin_Docker_ExchangeUsesResolvedHostname pins that the API client is
// built from the same hostname the domain is recorded under. The two are
// resolved from one variable, so an exchange can never verify access against a
// different host than the one being configured.
func TestLogin_Docker_ExchangeUsesResolvedHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{name: "empty hostname resolves to the default", hostname: "", want: "gitlab.default.example.com"},
		{name: "explicit hostname is used as given", hostname: "gitlab.other.example.com", want: "gitlab.other.example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			o := &options{
				credHelperApiClient: func(repoHost string) (*api.Client, error) {
					got = repoHost
					// Stop before the exchange: the host the client is built
					// for is all this test is about.
					return nil, errors.New("no API client in this test")
				},
				defaultHostname: "gitlab.default.example.com",
				hostname:        tc.hostname,
				registry:        "registry.example.com",
				docker:          true,
			}

			require.Error(t, o.run(t.Context()))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLogin_Docker_DurationIgnoredWarning(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv := dockerTokenServer(t)
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	out, err := exec("--docker --registry registry.example.com --duration 30m")
	require.NoError(t, err)
	assert.Contains(t, out.ErrBuf.String(), "--duration is ignored")
}

func TestLogin_Docker_NoDurationWarningWhenFlagNotPassed(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv := dockerTokenServer(t)
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	out, err := exec("--docker --registry registry.example.com")
	require.NoError(t, err)

	// The warning keys off cmd.Flags().Changed("duration"), not off the value,
	// so `--duration 0` still warns even though it matches the flag's default.
	assert.NotContains(t, out.ErrBuf.String(), "--duration is ignored")
}

// TestLogin_Docker_ExplicitZeroDurationStillWarns pins that the warning
// follows Changed, not the value. The flag defaults to zero because nothing
// reads it yet, so a value test would have gone quiet on `--duration 0`.
func TestLogin_Docker_ExplicitZeroDurationStillWarns(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv := dockerTokenServer(t)
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	out, err := exec("--docker --registry registry.example.com --duration 0")
	require.NoError(t, err)
	assert.Contains(t, out.ErrBuf.String(), "--duration is ignored")
}

// TestLogin_Docker_DurationOutOfRangeIsStillIgnored pins that the value is not
// range-checked on this path. --duration is never sent anywhere here, so
// rejecting a value the command has already said it ignores would contradict
// the warning. Step 5 adds the check with the first path that spends the value.
func TestLogin_Docker_DurationOutOfRangeIsStillIgnored(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, t.TempDir())

	srv := dockerTokenServer(t)
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	out, err := exec("--docker --registry registry.example.com --duration 100h")
	require.NoError(t, err)
	assert.Contains(t, out.ErrBuf.String(), "--duration is ignored")
}

// TestLogin_Docker_GlabNotOnPathFailsBeforeTheExchange pins that a glab PATH
// cannot resolve is rejected in validate, not by dockercredhelper.Install
// after run has already exchanged a token. The shim is written next to glab
// and shells out to it, so this login can never work, and it must not mint a
// live credential to find that out.
//
// The request counter is the assertion that matters: the error alone would
// also pass if Install were still the one raising it. loginDocker's own PATH
// failure, for callers that reach it without validate, is exercised in
// docker_test.go's TestLoginDocker_GlabNotOnPath.
func TestLogin_Docker_GlabNotOnPathFailsBeforeTheExchange(t *testing.T) {
	setPath(t, t.TempDir())
	setHome(t, t.TempDir())

	srv, requests := artifactregistrytest.NewTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	exec := newTestExec(t, newServerConfig(t, srv, glinstance.DefaultHostname))

	_, err := exec("--docker --registry registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "glab")
	assert.Zero(t, requests.Load(), "a login that can never work must not exchange a token")
}

// TestLogin_Docker_BadRegistryIsReportedAheadOfThePlatform pins the order
// inside validate: the environment checks run last, so a user who typed a
// registry they can fix hears about the flag rather than about their platform.
func TestLogin_Docker_BadRegistryIsReportedAheadOfThePlatform(t *testing.T) {
	o := &options{
		supportedOS: func() error { return errors.New(`operating system "windows" is not supported`) },
		registry:    "registry.example.com/some/path",
		docker:      true,
	}

	err := o.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain a path")
}
