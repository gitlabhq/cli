//go:build !integration

package ask

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestAskCmd(t *testing.T) {
	t.Skip("flaky, hits 10m timeout")

	initialAiResponse := "The appropriate ```git log --pretty=format:'%h'``` Git command ```non-git cmd``` for listing ```git show``` commit SHAs."
	outputWithoutExecution := "Commands:\n" + `
git log --pretty=format:'%h'
non-git cmd
git show

Explanation:

The appropriate git log --pretty=format:'%h' Git command non-git cmd for listing git show commit SHAs.

`

	tests := []struct {
		desc                       string
		content                    string
		withPrompt                 bool
		withExecution              bool
		withGlInstanceHostname     string
		expectedResult             []string
		expectedGlInstanceHostname string
	}{
		{
			desc:                       "agree to run commands",
			content:                    initialAiResponse,
			withGlInstanceHostname:     "",
			withPrompt:                 true,
			withExecution:              true,
			expectedResult:             []string{outputWithoutExecution, "git log executed", "git show executed"},
			expectedGlInstanceHostname: glinstance.DefaultHostname,
		},
		{
			desc:                       "disagree to run commands",
			content:                    initialAiResponse,
			withGlInstanceHostname:     "example.com",
			withPrompt:                 true,
			withExecution:              false,
			expectedResult:             []string{outputWithoutExecution},
			expectedGlInstanceHostname: "example.com",
		},
		{
			desc:                       "no commands",
			content:                    "There are no Git commands related to the text.",
			withGlInstanceHostname:     "instance.example.com",
			withPrompt:                 false,
			withExecution:              false,
			expectedResult:             []string{"Commands:\n\n\nExplanation:\n\nThere are no Git commands related to the text.\n\n"},
			expectedGlInstanceHostname: "instance.example.com",
		},
	}
	cmdLogResult := "git log executed"
	cmdShowResult := "git show executed"

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			body := `{"predictions": [{ "candidates": [ {"content": "` + tc.content + `"} ]}]}`

			// Create test server for the AI endpoint
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v4/ai/llm/git_command" {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(body))
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer testServer.Close()

			if tc.withPrompt {
				cs, restore := test.InitCmdStubber()
				defer restore()
				cs.Stub(cmdLogResult)
				cs.Stub(cmdShowResult)
			}

			// Create a GitLab client with the test server's URL
			gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
			require.NoError(t, err)

			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
				},
				api.WithGitLabClient(gitlabClient),
			)
			require.NoError(t, err)

			opts := []cmdtest.FactoryOption{
				func(f *cmdtest.Factory) {
					f.ApiClientStub = func(repoHost string) (*api.Client, error) {
						require.Equal(t, tc.expectedGlInstanceHostname, repoHost)
						return apiClient, nil
					}
				},
				cmdtest.WithBaseRepo("OWNER", "REPO", tc.withGlInstanceHostname),
			}

			// Set up prompt stub if needed
			if tc.withPrompt {
				c := ugh.New(t)
				if !tc.withExecution {
					c.Expect(ugh.Confirm(runCmdsQuestion)).
						Do(ugh.Reject)
				} else {
					c.Expect(ugh.Confirm(runCmdsQuestion)).
						Do(ugh.Affirm).
						Expect(ugh.ConfirmRegexp(`Run ` + "`.*?`")).
						Do(ugh.Affirm).
						Expect(ugh.ConfirmRegexp(`Run ` + "`.*?`")).
						Do(ugh.Affirm)
				}
				opts = append(opts, cmdtest.WithConsole(t, c))
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmdAsk, false, opts...)

			output, err := exec("git list 10 commits")
			require.NoError(t, err)

			stdout := output.String()
			for _, r := range tc.expectedResult {
				assert.Contains(t, stdout, r)
			}
			require.Empty(t, output.Stderr())
		})
	}
}

func TestFailedHttpResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc        string
		code        int
		response    string
		expectedMsg string
	}{
		{
			desc:        "API error",
			code:        http.StatusNotFound,
			response:    `{"message": "Error message"}`,
			expectedMsg: "404 Not Found",
		},
		{
			desc:        "Empty response",
			code:        http.StatusOK,
			response:    `{"choices": []}`,
			expectedMsg: aiResponseErr,
		},
		{
			desc:        "Bad JSON",
			code:        http.StatusOK,
			response:    `{"choices": [{"message": {"content": "hello"}}]}`,
			expectedMsg: aiResponseErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			// Create test server for the AI endpoint
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v4/ai/llm/git_command" {
					w.WriteHeader(tc.code)
					_, _ = w.Write([]byte(tc.response))
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer testServer.Close()

			// Create a GitLab client with the test server's URL
			gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
			require.NoError(t, err)

			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
				},
				api.WithGitLabClient(gitlabClient),
			)
			require.NoError(t, err)

			exec := cmdtest.SetupCmdForTest(t, NewCmdAsk, false,
				cmdtest.WithApiClient(apiClient),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			_, err = exec("git list 10 commits")
			require.EqualError(t, err, tc.expectedMsg)
		})
	}
}
