//go:build !integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	apiCmd "gitlab.com/gitlab-org/cli/internal/commands/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// telemetryServer wires the factory to a server that records the request the
// hook actually puts on the wire. The send bypasses client-go's typed service
// so it can carry project_path, which makes the wire format worth asserting.
func telemetryServer(t *testing.T, opts ...cmdtest.FactoryOption) (cmdutils.Factory, func() []trackEventOptions) {
	t.Helper()

	// Recorded rather than asserted in the handler: it runs on the server's
	// goroutine, where require must not be called.
	type recorded struct {
		method string
		path   string
		body   trackEventOptions
		err    error
	}

	var mu sync.Mutex
	var seen []recorded

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body trackEventOptions
		err := json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		seen = append(seen, recorded{method: r.Method, path: r.URL.Path, body: body, err: err})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	// The default base URL is https; the test server speaks http.
	client := cmdtest.NewTestApiClient(t, srv.Client(), "token", host, api.WithBaseURL(srv.URL+"/api/v4"))

	factoryOpts := append([]cmdtest.FactoryOption{cmdtest.WithGitLabClient(client.Lab())}, opts...)

	return cmdtest.NewTestFactory(ios, factoryOpts...), func() []trackEventOptions {
		t.Helper()

		mu.Lock()
		defer mu.Unlock()

		events := make([]trackEventOptions, 0, len(seen))
		for _, rec := range seen {
			require.NoError(t, rec.err, "telemetry body must be valid JSON")
			require.Equal(t, http.MethodPost, rec.method)
			require.Equal(t, "/api/v4/usage_data/track_event", rec.path)
			events = append(events, rec.body)
		}
		return events
	}
}

func Test_sendTelemetryData(t *testing.T) {
	tests := []struct {
		name        string
		cobraMocks  []*cobra.Command
		command     string
		subcommand  string
		fullCommand string
	}{
		{
			name:        "command with subcommand",
			cobraMocks:  []*cobra.Command{{Use: "glab"}, {Use: "mr"}, {Use: "view"}},
			command:     "mr",
			subcommand:  "view",
			fullCommand: "mr view",
		},
		{
			name:        "command with multiple subcommands",
			cobraMocks:  []*cobra.Command{{Use: "glab"}, {Use: "command"}, {Use: "subcommand1"}, {Use: "subcommand2"}},
			command:     "command",
			subcommand:  "subcommand1 subcommand2",
			fullCommand: "command subcommand1 subcommand2",
		},
		{
			name:        "single command only",
			cobraMocks:  []*cobra.Command{{Use: "glab"}, {Use: "version"}},
			command:     "version",
			subcommand:  "",
			fullCommand: "version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, sent := telemetryServer(t)

			passedCommand := tt.cobraMocks[0]
			for i, cmd := range tt.cobraMocks {
				if i > 0 {
					tt.cobraMocks[i-1].AddCommand(cmd)
					passedCommand = cmd
				}
			}

			sendTelemetryData(f, passedCommand)

			require.Equal(t, []trackEventOptions{{
				Event:          "gitlab_cli_command_used",
				ProjectPath:    "OWNER/REPO",
				SendToSnowplow: new(true),
				AdditionalProperties: map[string]string{
					"label":                  tt.command,
					"property":               tt.subcommand,
					"command_and_subcommand": tt.fullCommand,
					"cli_version":            "test",
					"platform":               runtime.GOOS,
					"architecture":           runtime.GOARCH,
				},
			}}, sent())
		})
	}
}

func Test_buildTelemetryEvent_codingAgent(t *testing.T) {
	tests := []struct {
		name        string
		codingAgent string
		want        string
		wantPresent bool
	}{
		{name: "agent detected", codingAgent: "claude-code", want: "claude-code", wantPresent: true},
		{name: "no agent detected", codingAgent: "", wantPresent: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := telemetryServer(t, cmdtest.WithBuildInfo(api.BuildInfo{
				Version:      "v1.2.3",
				Platform:     "linux",
				Architecture: "amd64",
				CodingAgent:  tt.codingAgent,
			}))

			_, event, ok := buildTelemetryEvent(f, "version", "", "version")
			require.True(t, ok)

			agent, present := event.AdditionalProperties["coding_agent"]
			require.Equal(t, tt.wantPresent, present)
			require.Equal(t, tt.want, agent)

			require.Equal(t, "v1.2.3", event.AdditionalProperties["cli_version"])
			require.Equal(t, "linux", event.AdditionalProperties["platform"])
			require.Equal(t, "amd64", event.AdditionalProperties["architecture"])
		})
	}
}

func Test_addTelemetryHook_blocksUntilEventIsSent(t *testing.T) {
	// The hook used to spawn an unjoined goroutine, so glab exited before the
	// event was sent for every command that did not linger afterwards.
	f, sent := telemetryServer(t)

	root := &cobra.Command{Use: "glab"}
	version := &cobra.Command{Use: "version"}
	root.AddCommand(version)

	addTelemetryHook(f, version)()

	require.Len(t, sent(), 1, "hook returned before the telemetry event was sent")
}

func requestDeadline(t *testing.T, opts []gitlab.RequestOptionFunc) (time.Time, bool) {
	t.Helper()

	req, err := retryablehttp.NewRequest(http.MethodGet, "https://gitlab.example.com", nil)
	require.NoError(t, err)
	for _, opt := range opts {
		require.NoError(t, opt(req))
	}
	return req.Context().Deadline()
}

func Test_telemetryRequestOptions_boundsTheRequest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), telemetrySendTimeout)
	defer cancel()

	deadline, bounded := requestDeadline(t, telemetryRequestOptions(ctx))

	require.True(t, bounded, "the request must carry a deadline so it cannot hold up exit")
	require.False(t, deadline.After(time.Now().Add(telemetrySendTimeout)), "deadline exceeds telemetrySendTimeout")
}

func Test_sendTelemetryData_skipsMachineInvokedCommands(t *testing.T) {
	// These run from shell startup files and from git. A blocking network call
	// there is exactly what ShouldSkipUpdate already avoids.
	for _, path := range []string{"completion", "auth git-credential", "auth credential-helper"} {
		t.Run(path, func(t *testing.T) {
			f, sent := telemetryServer(t)

			cmd := &cobra.Command{Use: "glab"}
			for name := range strings.SplitSeq(path, " ") {
				child := &cobra.Command{Use: name}
				cmd.AddCommand(child)
				cmd = child
			}

			sendTelemetryData(f, cmd)

			require.Empty(t, sent(), "no event may be sent for a machine-invoked command")
		})
	}
}

func Test_buildTelemetryEvent_sendsProjectPath(t *testing.T) {
	t.Parallel()

	// The instance resolves the path to project and namespace IDs, so glab
	// never has to look them up.
	t.Run("inside a repo", func(t *testing.T) {
		t.Parallel()

		f, _ := telemetryServer(t)

		_, event, ok := buildTelemetryEvent(f, "version", "", "version")
		require.True(t, ok)

		require.Equal(t, "OWNER/REPO", event.ProjectPath)
	})

	t.Run("outside a repo", func(t *testing.T) {
		t.Parallel()

		f, _ := telemetryServer(t, cmdtest.WithBaseRepoError(errors.New("no remotes")))

		_, event, ok := buildTelemetryEvent(f, "version", "", "version")
		require.True(t, ok)

		require.Empty(t, event.ProjectPath)
	})
}

func Test_trackEventOptions_encoding(t *testing.T) {
	t.Parallel()

	// project_path and project_id are mutually exclusive on the endpoint, so an
	// empty path must be omitted rather than sent as "".
	body, err := json.Marshal(&trackEventOptions{
		Event:          "gitlab_cli_command_used",
		SendToSnowplow: new(true),
		AdditionalProperties: map[string]string{
			"label": "version",
		},
	})
	require.NoError(t, err)
	require.NotContains(t, string(body), "project_path")

	body, err = json.Marshal(&trackEventOptions{
		Event:       "gitlab_cli_command_used",
		ProjectPath: "OWNER/REPO",
	})
	require.NoError(t, err)
	require.Contains(t, string(body), `"project_path":"OWNER/REPO"`)
}

func Test_noRetry(t *testing.T) {
	t.Parallel()

	// The client retries five times by default. For telemetry that turns an
	// instant failure, such as a refused connection, into the full timeout on
	// every command.
	t.Run("never retries", func(t *testing.T) {
		t.Parallel()

		retry, err := noRetry(t.Context(), nil, errors.New("connection refused"))

		require.False(t, retry)
		require.NoError(t, err)
	})

	t.Run("surfaces context cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		retry, err := noRetry(ctx, nil, nil)

		require.False(t, retry)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func Test_telemetryRequestOptions_disablesRetries(t *testing.T) {
	t.Parallel()

	// Guards the pairing: a deadline alone still leaves retries burning it.
	require.Len(t, telemetryRequestOptions(t.Context()), 2)
}

func Test_templateEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "no parameters", path: "/user", want: "/user"},
		{name: "leading slash optional", path: "user", want: "/user"},
		{name: "numeric project id", path: "projects/278964/merge_requests", want: "/projects/:id/merge_requests"},
		{name: "encoded project path", path: "projects/gitlab-org%2Fcli/issues", want: "/projects/:id/issues"},
		{name: "glab placeholder", path: "projects/:fullpath/merge_requests", want: "/projects/:id/merge_requests"},
		{name: "nested parameters", path: "projects/1/merge_requests/2/notes", want: "/projects/:id/merge_requests/:id/notes"},
		{name: "trailing slash", path: "/projects/1/issues/", want: "/projects/:id/issues"},
		{name: "uppercase segment", path: "/Projects/1/Issues", want: "/projects/:id/issues"},
		{name: "graphql", path: "graphql", want: "/graphql"},

		// Indistinguishable from route nouns by shape: a heuristic templater
		// would keep these verbatim.
		{name: "username", path: "users/phikai/projects", want: "/users/:id/projects"},
		{name: "branch name", path: "projects/1/repository/branches/my-feature", want: "/projects/:id/repository/branches/:id"},
		{name: "file path", path: "projects/1/repository/files/src%2Fmain.go/raw", want: "/projects/:id/repository/files/:id/raw"},

		{name: "unknown endpoint", path: "projects/1/not_a_real_resource", want: unmatchedEndpoint},
		{name: "empty", path: "", want: unmatchedEndpoint},
		{name: "root", path: "/", want: unmatchedEndpoint},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, templateEndpoint(tt.path))
		})
	}
}

func Test_templateEndpoint_neverEmitsRequestData(t *testing.T) {
	t.Parallel()

	// Every value is either a route client-go knows or the sentinel, so no
	// input can survive into the payload.
	paths := []string{
		"projects/1/issues?private_token=glpat-SECRET&search=confidential",
		"projects/1/issues#fragment",
		"users/phikai/projects",
		"projects/1/repository/branches/release%2F17-0",
		"projects/1/not_a_real_resource/glpat-SECRET",
		"../../etc/passwd",
	}

	for _, path := range paths {
		got := templateEndpoint(path)
		assert.NotContains(t, got, "glpat", path)
		assert.NotContains(t, got, "phikai", path)
		assert.NotContains(t, got, "confidential", path)
		assert.NotContains(t, got, "passwd", path)

		if got != unmatchedEndpoint {
			assert.True(t, slices.ContainsFunc(gitlab.Routes(), func(r gitlab.Route) bool {
				return r.String() == got
			}), "%q produced %q, which is not a route the client knows", path, got)
		}
	}
}

func Test_apiEventProperties(t *testing.T) {
	t.Parallel()

	t.Run("reports endpoint and method", func(t *testing.T) {
		t.Parallel()

		cmd := parseAPICommand(t, []string{"projects/278964/merge_requests", "--field", "title=x"})

		assert.Equal(t, map[string]string{
			"api_endpoint": "/projects/:id/merge_requests",
			"http_method":  "POST",
		}, apiEventProperties(cmd))
	})

	t.Run("nil for other commands", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, apiEventProperties(&cobra.Command{Use: "mr"}))
	})

	t.Run("nil when no endpoint was given", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, apiEventProperties(parseAPICommand(t, nil)))
	})
}

// parseAPICommand parses args into the real command, so a renamed flag fails
// here rather than silently in production.
func parseAPICommand(t *testing.T, args []string) *cobra.Command {
	t.Helper()

	ios, _, _, _ := cmdtest.TestIOStreams()
	cmd := apiCmd.NewCmdApi(cmdtest.NewTestFactory(ios), nil)

	require.NoError(t, cmd.Flags().Parse(args))
	return cmd
}

func Test_parseCommand(t *testing.T) {
	tests := []struct {
		name        string
		cmdString   []string
		command     string
		subcommand  string
		fullCommand string
	}{
		{
			name:        "basic command",
			cmdString:   []string{"glab", "mr", "list"},
			command:     "mr",
			subcommand:  "list",
			fullCommand: "mr list",
		},
		{
			name:        "multiple subcommands",
			cmdString:   []string{"glab", "command", "subcommand1", "subcommand2", "subcommand3"},
			command:     "command",
			subcommand:  "subcommand1 subcommand2 subcommand3",
			fullCommand: "command subcommand1 subcommand2 subcommand3",
		},
		{
			name:        "no subcommand",
			cmdString:   []string{"glab", "mr"},
			command:     "mr",
			subcommand:  "",
			fullCommand: "mr",
		},
		{
			name:        "too short of a command",
			cmdString:   []string{"glab"},
			command:     "",
			subcommand:  "",
			fullCommand: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, subcommand, fullCommand := parseCommand(tt.cmdString)

			require := require.New(t)
			require.Equal(tt.command, command)
			require.Equal(tt.subcommand, subcommand)
			require.Equal(tt.fullCommand, fullCommand)
		})
	}
}

func TestIsTelemetryEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		configYaml     string
		expectedResult bool
	}{
		{
			name:           "enabled with 'true' value",
			configYaml:     "telemetry: true",
			expectedResult: true,
		},
		{
			name:           "enabled with '1' value",
			configYaml:     "telemetry: '1'",
			expectedResult: true,
		},
		{
			name:           "disabled with 'false' value",
			configYaml:     "telemetry: false",
			expectedResult: false,
		},
		{
			name:           "disabled with '0' value",
			configYaml:     "telemetry: '0'",
			expectedResult: false,
		},
		{
			name:           "enabled with empty value",
			configYaml:     "telemetry: ''",
			expectedResult: true,
		},
		{
			name:           "enabled with other value",
			configYaml:     "telemetry: something",
			expectedResult: true,
		},
		{
			name:           "no config value set",
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var cfg config.Config
			if tt.configYaml != "" {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(tt.configYaml), 0o600))
				var err error
				cfg, err = config.ParseConfig(filepath.Join(dir, "config.yml"))
				require.NoError(t, err)
			} else {
				cfg = config.NewBlankConfig()
			}

			result := isTelemetryEnabled(cfg)

			require.Equal(t, tt.expectedResult, result)
		})
	}
}
