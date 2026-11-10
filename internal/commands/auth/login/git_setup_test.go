//go:build !integration

package login

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

// recordGitCommands captures the commands a login runs, so the tests assert on
// the Git configuration glab writes instead of running Git.
func recordGitCommands(t *testing.T, gitConfig map[string]string) *[][]string {
	t.Helper()

	var calls [][]string
	teardown := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		calls = append(calls, cmd.Args)

		// `git config <key>` reads a value: answer from the fake configuration
		// so the credential helper check sees a realistic host state.
		if len(cmd.Args) == 3 && cmd.Args[1] == "config" {
			if value, ok := gitConfig[cmd.Args[2]]; ok {
				return &test.OutputStub{Out: []byte(value + "\n")}
			}
			return &test.OutputStub{Error: &exec.ExitError{}}
		}

		// The web login opens the authorization URL in a browser. Stand in for
		// the user who approves it, so the login receives its callback.
		if authURL := cmd.Args[len(cmd.Args)-1]; strings.Contains(authURL, "/oauth/authorize?") {
			approveAuthorization(t, authURL)
		}
		return &test.OutputStub{}
	})
	t.Cleanup(teardown)

	return &calls
}

func approveAuthorization(t *testing.T, authURL string) {
	t.Helper()

	u, err := url.Parse(authURL)
	require.NoError(t, err)
	query := u.Query()

	callback := fmt.Sprintf("%s?code=test-code&state=%s", query.Get("redirect_uri"), url.QueryEscape(query.Get("state")))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, callback, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// newOAuthServer stands in for the OAuth endpoints of a GitLab instance, for
// the --web and --device logins.
func newOAuthServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/authorize_device":
			fmt.Fprint(w, `{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":"https://gitlab.example.com/oauth/device","expires_in":300,"interval":1}`)
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"oauth-token","token_type":"bearer","expires_in":7200,"refresh_token":"refresh-token"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// oauthLoginExec runs `glab auth login` against srv. As a self-managed
// instance, srv needs a client_id, and the OAuth client must accept its
// test certificate.
func oauthLoginExec(t *testing.T, srv *httptest.Server) (cmdtest.CmdExecFunc, string) {
	t.Helper()

	hostname := srv.Listener.Addr().String()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockUsers.EXPECT().
		CurrentUser(gomock.Any()).
		Return(&gitlab.User{Username: "john_smith"}, nil, nil).
		AnyTimes()

	cfg := config.NewBlankConfigInDir(t.TempDir())
	require.NoError(t, cfg.Set(hostname, "client_id", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	require.NoError(t, cfg.Set(hostname, "skip_tls_verify", "true"))

	return loginExec(t, false,
		cmdtest.WithConfig(cfg),
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, srv.Client(), "", hostname, api.WithGitLabClient(tc.Client))),
	), hostname
}

func loginExec(t *testing.T, isTTY bool, opts ...cmdtest.FactoryOption) cmdtest.CmdExecFunc {
	t.Helper()
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())

	opts = append([]cmdtest.FactoryOption{cmdtest.WithConfig(config.NewBlankConfigInDir(t.TempDir()))}, opts...)
	return cmdtest.SetupCmdForTest(t, NewCmdLogin, isTTY, opts...)
}

// findCall returns the first recorded invocation that contains every fragment.
func findCall(calls [][]string, fragments ...string) []string {
	for _, call := range calls {
		joined := strings.Join(call, " ")
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(joined, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return call
		}
	}
	return nil
}

func countCalls(calls [][]string, fragments ...string) int {
	count := 0
	for _, call := range calls {
		if findCall([][]string{call}, fragments...) != nil {
			count++
		}
	}
	return count
}

func Test_gitCredentialProtocol(t *testing.T) {
	tests := []struct {
		name        string
		gitProtocol string
		want        string
	}{
		{name: "empty defaults to https", gitProtocol: "", want: "https"},
		{name: "https stays https", gitProtocol: "https", want: "https"},
		{name: "http is preserved", gitProtocol: "http", want: "http"},
		{name: "uppercase http is preserved", gitProtocol: "HTTP", want: "http"},
		// A credential helper never applies to SSH remotes, so HTTPS is the
		// only protocol worth configuring for an SSH preference.
		{name: "ssh falls back to https", gitProtocol: "ssh", want: "https"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, gitCredentialProtocol(tt.gitProtocol))
		})
	}
}

func Test_tokenLogin_setupGitCredentialHelper(t *testing.T) {
	keyring.MockInit()

	t.Run("configures glab as the helper for the host", func(t *testing.T) {
		calls := recordGitCommands(t, nil)

		_, err := loginExec(t, false, currentUserFactoryOption(t))(
			"--hostname gitlab.com --token glpat-1234 --setup-git-credential-helper")
		require.NoError(t, err)

		key := "credential.https://gitlab.com.helper"
		assert.Equal(t,
			[]string{"git", "config", "--global", "--replace-all", key, ""},
			findCall(*calls, "config", "--global", "--replace-all", key))

		add := findCall(*calls, "config", "--global", "--add", key)
		require.NotNil(t, add, "glab should be added as the helper")
		assert.Contains(t, strings.Join(add, " "), "auth git-credential")
	})

	t.Run("honors --git-protocol http", func(t *testing.T) {
		calls := recordGitCommands(t, nil)

		_, err := loginExec(t, false, currentUserFactoryOption(t))(
			"--hostname gitlab.example.com --token glpat-1234 --git-protocol http --setup-git-credential-helper")
		require.NoError(t, err)

		assert.NotNil(t, findCall(*calls, "--add", "credential.http://gitlab.example.com.helper"))
	})

	t.Run("leaves a host that already uses glab alone", func(t *testing.T) {
		calls := recordGitCommands(t, map[string]string{
			"credential.https://gitlab.com.helper": "!/usr/bin/glab auth git-credential",
		})

		_, err := loginExec(t, false, currentUserFactoryOption(t))(
			"--hostname gitlab.com --token glpat-1234 --setup-git-credential-helper")
		require.NoError(t, err)

		assert.Nil(t, findCall(*calls, "config", "--global"),
			"an existing glab helper should not be configured again")
	})

	t.Run("writes nothing without the flag", func(t *testing.T) {
		calls := recordGitCommands(t, nil)

		_, err := loginExec(t, false, currentUserFactoryOption(t))(
			"--hostname gitlab.com --token glpat-1234")
		require.NoError(t, err)

		assert.Nil(t, findCall(*calls, "credential.https://gitlab.com.helper"))
	})
}

func Test_stdinLogin_setupGitCredentialHelper(t *testing.T) {
	keyring.MockInit()
	calls := recordGitCommands(t, nil)

	_, err := loginExec(t, false, currentUserFactoryOption(t), cmdtest.WithStdin("glpat-1234\n"))(
		"--hostname gitlab.com --stdin --setup-git-credential-helper")
	require.NoError(t, err)

	assert.NotNil(t, findCall(*calls, "config", "--global", "--add", "credential.https://gitlab.com.helper"))
}

// A job token cannot identify a person, but Git still needs to authenticate.
func Test_jobTokenLogin_setupGitCredentialHelper(t *testing.T) {
	keyring.MockInit()
	calls := recordGitCommands(t, nil)

	_, err := loginExec(t, false, currentUserFactoryOption(t))(
		"--hostname gitlab.com --job-token job-token-1234 --setup-git-credential-helper")
	require.NoError(t, err)

	assert.NotNil(t, findCall(*calls, "config", "--global", "--add", "credential.https://gitlab.com.helper"))
}

func Test_webLogin_setupGitCredentialHelper(t *testing.T) {
	keyring.MockInit()
	calls := recordGitCommands(t, nil)

	execLogin, hostname := oauthLoginExec(t, newOAuthServer(t))
	_, err := execLogin("--hostname " + hostname + " --web --setup-git-credential-helper")
	require.NoError(t, err)

	assert.NotNil(t, findCall(*calls, "config", "--global", "--add", "credential.https://"+hostname+".helper"))
}

func Test_deviceLogin_setupGitCredentialHelper(t *testing.T) {
	keyring.MockInit()

	t.Run("configures glab as the helper for the host", func(t *testing.T) {
		calls := recordGitCommands(t, nil)

		execLogin, hostname := oauthLoginExec(t, newOAuthServer(t))
		_, err := execLogin("--hostname " + hostname + " --device --setup-git-credential-helper")
		require.NoError(t, err)

		assert.NotNil(t, findCall(*calls, "config", "--global", "--add", "credential.https://"+hostname+".helper"))
	})

	t.Run("honors --git-protocol http", func(t *testing.T) {
		calls := recordGitCommands(t, nil)

		execLogin, hostname := oauthLoginExec(t, newOAuthServer(t))
		_, err := execLogin("--hostname " + hostname + " --device --git-protocol http --setup-git-credential-helper")
		require.NoError(t, err)

		assert.NotNil(t, findCall(*calls, "config", "--global", "--add", "credential.http://"+hostname+".helper"))
	})
}

func Test_interactiveLogin_setupGitCredentialHelper(t *testing.T) {
	// Every other prompt is answered by a flag, so the console only has to
	// pick the token login and paste the token.
	const args = "--hostname gitlab.com --git-protocol https --container-registry-domains gitlab.com"

	t.Run("the flag configures glab without asking", func(t *testing.T) {
		// A token left in the keyring by another test would add the
		// re-authentication prompt.
		keyring.MockInit()

		// Another helper is configured, which is the case where answering the
		// prompt would only store the token in that helper.
		calls := recordGitCommands(t, map[string]string{"credential.helper": "store"})

		console := ugh.New(t)
		console.Expect(ugh.Select("How would you like to sign in?")).Do(ugh.SelectOption(promptLoginTypeToken))
		console.Expect(ugh.Input("Paste your authentication token:")).Do(ugh.Type("glpat-1234"))

		_, err := loginExec(t, true, currentUserFactoryOption(t), cmdtest.WithConsole(t, console))(
			args + " --setup-git-credential-helper")
		require.NoError(t, err)

		assert.Equal(t, 1, countCalls(*calls, "config", "--global", "--add", "credential.https://gitlab.com.helper"))
		assert.Nil(t, findCall(*calls, "credential", "approve"))
	})

	t.Run("answering yes configures glab", func(t *testing.T) {
		keyring.MockInit()
		calls := recordGitCommands(t, nil)

		console := ugh.New(t)
		console.Expect(ugh.Select("How would you like to sign in?")).Do(ugh.SelectOption(promptLoginTypeToken))
		console.Expect(ugh.Input("Paste your authentication token:")).Do(ugh.Type("glpat-1234"))
		console.Expect(ugh.Confirm("Authenticate Git with your GitLab credentials?")).Do(ugh.Affirm)

		_, err := loginExec(t, true, currentUserFactoryOption(t), cmdtest.WithConsole(t, console))(args)
		require.NoError(t, err)

		assert.Equal(t, 1, countCalls(*calls, "config", "--global", "--add", "credential.https://gitlab.com.helper"))
	})
}
