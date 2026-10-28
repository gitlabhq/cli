//go:build !integration

package sync

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/opencodeplugin"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const testHost = "gitlab.example.com"

// fakeGitLab serves the project lookups and AI agent governance endpoints.
// It is an HTTP server rather than a client-go mock because the governance
// endpoints are raw requests that client-go has no service for.
type fakeGitLab struct {
	mu               gosync.Mutex
	projects         map[string]int64
	projectLookups   map[string]int
	sessionsCreated  []string
	agentTypes       []string
	syncTypes        []string
	identityRequests int
	eventBatches     []int
	events           []auditEventRequest
	eventsPosted     int
	completed        int
	// noSessionID makes session creation succeed without returning an ID.
	noSessionID bool
	// identityStatus and sessionStatus, when set, answer those endpoints with
	// that HTTP status. GitLab answers 404 while AI agent governance is off
	// for the project.
	identityStatus int
	sessionStatus  int
	// projectStatus, when set, answers lookups of unknown projects with that
	// HTTP status instead of 404.
	projectStatus int
	// identityBody, when set, is the body sent with identityStatus.
	identityBody string
	// maxElements, when set, replaces the 5,000 JSON elements GitLab accepts
	// in an audit events request.
	maxElements int
	// auditStatus, when set, answers audit event requests with that status.
	auditStatus int
	// tooLargeToolCall, when set, makes any audit events request with an event
	// for that tool call exceed GitLab's JSON limits.
	tooLargeToolCall string
	auditRequests    int
	// rateLimited is how many audit event requests GitLab answers with 429
	// before it accepts them.
	rateLimited int
	// retryAfter is the Retry-After GitLab sends with a 429, "0" if unset.
	retryAfter string
	// auditRejections counts the audit event requests over GitLab's limits.
	auditRejections int
	// badGateway is how many 502s each write endpoint ("session", "audit",
	// "complete") answers with before it accepts requests.
	badGateway map[string]int
}

// failWithBadGateway answers with a 502 if endpoint has one left to send.
func (f *fakeGitLab) failWithBadGateway(w http.ResponseWriter, endpoint string) bool {
	if f.badGateway[endpoint] == 0 {
		return false
	}
	f.badGateway[endpoint]--
	writeStatus(w, http.StatusBadGateway)
	return true
}

func (f *fakeGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	project, isProjectLookup := strings.CutPrefix(r.URL.EscapedPath(), "/api/v4/projects/")
	isProjectLookup = isProjectLookup && r.Method == http.MethodGet && !strings.Contains(project, "/")
	switch {
	case isProjectLookup:
		name, _ := url.PathUnescape(project)
		f.projectLookups[name]++
		id, ok := f.projects[name]
		if !ok && f.projectStatus != 0 {
			writeStatus(w, f.projectStatus)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"404 Project Not Found"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "path_with_namespace": name})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/ai_agent/identities"):
		f.identityRequests++
		if f.identityStatus != 0 {
			if f.identityBody != "" {
				w.WriteHeader(f.identityStatus)
				_, _ = io.WriteString(w, f.identityBody)
				return
			}
			writeStatus(w, f.identityStatus)
			return
		}
		_, _ = io.WriteString(w, `{"id": 7}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/ai_agent/sessions"):
		if f.failWithBadGateway(w, "session") {
			return
		}
		if f.sessionStatus != 0 {
			writeStatus(w, f.sessionStatus)
			return
		}
		var body sessionRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.sessionsCreated = append(f.sessionsCreated, body.IdempotencyKey)
		f.agentTypes = append(f.agentTypes, body.AgentType)
		f.syncTypes = append(f.syncTypes, body.SyncType)
		if f.noSessionID {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_, _ = io.WriteString(w, `{"id": 99}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/ai_agent/audit_events"):
		f.auditRequests++
		if f.failWithBadGateway(w, "audit") {
			return
		}
		if f.auditRequests <= f.rateLimited {
			// A Ratelimit-Reset in the past keeps the API client's own retries short.
			w.Header().Set("Ratelimit-Reset", "1")
			w.Header().Set("Retry-After", cmp.Or(f.retryAfter, "0"))
			writeStatus(w, http.StatusTooManyRequests)
			return
		}
		if f.auditStatus != 0 {
			writeStatus(w, f.auditStatus)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body auditEventsRequest
		_ = json.Unmarshal(raw, &body)
		if limit := f.exceededJSONLimit(raw, body); limit != "" {
			f.auditRejections++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":%q}`, limit)
			return
		}
		f.eventBatches = append(f.eventBatches, len(body.Events))
		f.events = append(f.events, body.Events...)
		f.eventsPosted += len(body.Events)
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodPatch && strings.HasSuffix(path, "/ai_agent/sessions/99"):
		if f.failWithBadGateway(w, "complete") {
			return
		}
		f.completed++
		_, _ = io.WriteString(w, `{}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// exceededJSONLimit returns the error GitLab's JSON validation answers an
// audit events request with, or "" if the request is within its limits.
func (f *fakeGitLab) exceededJSONLimit(raw []byte, body auditEventsRequest) string {
	maxElements := f.maxElements
	if maxElements == 0 {
		maxElements = 5000
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	switch {
	case len(body.Events) > maxEventsPerRequest:
		return "Array parameter too large"
	case 1+jsonElements(v) >= maxElements:
		return "Too many total parameters"
	case f.tooLargeToolCall != "" && slices.ContainsFunc(body.Events, func(e auditEventRequest) bool {
		return e.Details["tool_call_id"] == f.tooLargeToolCall
	}):
		return "Hash parameter too large"
	}
	return ""
}

func toolIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("toolu_%d", i)
	}
	return ids
}

func eventNames(events []auditEventRequest) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.EventName)
	}
	return names
}

func writeStatus(w http.ResponseWriter, code int) {
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"message":"%d %s"}`, code, http.StatusText(code))
}

type syncAllEnv struct {
	home  string
	fake  *fakeGitLab
	exec  cmdtest.CmdExecFunc
	mExec *cmdtest.MockExecutor
}

func newSyncAllEnv(t *testing.T, hooksInstalled bool) *syncAllEnv {
	t.Helper()
	return newSyncAllEnvWithConfig(t, hooksInstalled, "hosts:\n  "+testHost+":\n    token: test-token\n")
}

// newSyncAllEnvWithConfig is newSyncAllEnv with glab configuration cfg.
func newSyncAllEnvWithConfig(t *testing.T, hooksInstalled bool, cfg string) *syncAllEnv {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(home, "glab"))
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("OPENCODE_SESSION_ID", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	if hooksInstalled {
		installOpenCodePlugin(t)
		hooks := map[string][]claudehooks.HookGroup{}
		claudehooks.AddHook(hooks, "Stop", claudehooks.StopHookCommand)
		hooksJSON, err := json.Marshal(hooks)
		require.NoError(t, err)
		settings, err := claudehooks.SettingsPath()
		require.NoError(t, err)
		require.NoError(t, claudehooks.WriteSettings(settings, map[string]json.RawMessage{"hooks": hooksJSON}))
	}

	fake := &fakeGitLab{projects: map[string]int64{"my-group/my-project": 42}, projectLookups: map[string]int{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	client, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(server.URL+"/api/v4"), gitlab.WithOnlyIdempotentRetries())
	require.NoError(t, err)
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "test-token", testHost, api.WithGitLabClient(client))),
		cmdtest.WithExecutor(mExec),
		cmdtest.WithConfig(config.NewFromString(cfg)),
	)

	return &syncAllEnv{home: home, fake: fake, exec: exec, mExec: mExec}
}

// writeClaudeSession writes a Claude Code transcript containing one tool call
// per tool ID, and the metadata a hook records for it.
func (e *syncAllEnv) writeClaudeSession(t *testing.T, sessionID, project string, toolIDs ...string) string {
	t.Helper()

	content := claudeTranscriptLines(t, sessionID, toolIDs...)

	path := filepath.Join(e.home, ".claude", "projects", "-work-"+sessionID, sessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, content, 0o644))

	require.NoError(t, writeSessionMeta(sessionID, sessionMeta{
		AgentType:         "claude-code",
		PathWithNamespace: project,
		Host:              testHost,
		Transcript:        path,
	}))
	return path
}

func assertCursorAt(t *testing.T, sessionID string, want int64) {
	t.Helper()
	cursor, err := readCursor(sessionID)
	require.NoError(t, err)
	assert.Equal(t, want, cursor)
}

// recordWaits makes opts' waits return at once, and returns the waits asked for.
func recordWaits(opts *options) *[]time.Duration {
	var waits []time.Duration
	opts.waitFor = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return &waits
}

func assertCursorAtEnd(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assertCursorAt(t, strings.TrimSuffix(filepath.Base(path), ".jsonl"), info.Size())
}

func readStatus(t *testing.T) *fallbacksync.Status {
	t.Helper()
	status, err := fallbacksync.ReadStatus()
	require.NoError(t, err)
	return status
}

func TestSyncAll_UploadsRecordedSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1", "toolu_2")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"sess-1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"fallback"}, env.fake.syncTypes)
	assert.Equal(t, 2, env.fake.eventsPosted)
	assert.Zero(t, env.fake.completed)

	info, err := os.Stat(path)
	require.NoError(t, err)
	cursor, err := readCursor("sess-1")
	require.NoError(t, err)
	assert.Equal(t, info.Size(), cursor)

	status := readStatus(t)
	assert.Equal(t, 1, status.SessionsSynced)
	assert.Equal(t, 2, status.EventsPosted)
	assert.Empty(t, status.Errors)
}

func TestSyncAll_KeepsCursorWhenSessionHasNoID(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.noSessionID = true
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Zero(t, env.fake.eventsPosted)
	cursor, err := readCursor("sess-1")
	require.NoError(t, err)
	assert.Zero(t, cursor, "unposted events must be retried on the next run")

	status := readStatus(t)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], "no session ID")
}

func TestSyncAll_RecordsFailedIdentityRegistration(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.identityStatus = http.StatusForbidden
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.sessionsCreated, "GitLab requires an agent identity to create a session")
	cursor, err := readCursor("sess-1")
	require.NoError(t, err)
	assert.Zero(t, cursor, "the session is retried on the next run")
	status := readStatus(t)
	assert.Zero(t, status.SessionsSynced)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], "session sess-1: could not register agent identity")
}

func TestSyncAll_RegistersIdentityOncePerProject(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.identityStatus = http.StatusForbidden
	for _, id := range []string{"sess-1", "sess-2", "sess-3"} {
		env.writeClaudeSession(t, id, "my-group/my-project", "toolu_"+id)
	}

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 1, env.fake.identityRequests, "a failed registration is not repeated for each session")
	assert.Empty(t, env.fake.sessionsCreated)
	assert.Len(t, readStatus(t).Errors, 1)
}

func TestSyncAll_RecordsFailedProjectOnce(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projectStatus = http.StatusForbidden
	env.writeClaudeSession(t, "sess-1", "gone/project", "toolu_1")
	env.writeClaudeSession(t, "sess-2", "gone/project", "toolu_2")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 1, env.fake.projectLookups["gone/project"])
	status := readStatus(t)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], "gone/project")
}

func TestSyncAll_GovernanceNotEnabledIsNotAnError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity int
		session  int
	}{
		{name: "identity registration", identity: http.StatusNotFound},
		{name: "session creation", session: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.identityStatus = tc.identity
			env.fake.sessionStatus = tc.session
			env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
			env.writeClaudeSession(t, "sess-2", "my-group/my-project", "toolu_2")

			_, err := env.exec("--all")
			require.NoError(t, err)

			status := readStatus(t)
			assert.Empty(t, status.Errors, "a project without governance is expected, not a failure")
			assert.Equal(t, []string{testHost + "/my-group/my-project"}, status.GovernanceNotEnabled)
			cursor, err := readCursor("sess-1")
			require.NoError(t, err)
			assert.Zero(t, cursor, "the session syncs once governance is enabled")
		})
	}
}

func TestSyncAll_ProjectNotFoundIsNotAnError(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.writeClaudeSession(t, "sess-1", "gone/project", "toolu_1")
	env.writeClaudeSession(t, "sess-2", "gone/project", "toolu_2")
	env.writeClaudeSession(t, "sess-good", "my-group/my-project", "toolu_3")

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	assert.Empty(t, status.Errors, "a project the user can't see is expected, not a failure")
	assert.Equal(t, []string{testHost + "/gone/project"}, status.ProjectNotFound)
	assert.Equal(t, []string{"sess-good"}, env.fake.sessionsCreated)
	assert.Equal(t, 1, env.fake.projectLookups["gone/project"])
	for _, id := range []string{"sess-1", "sess-2"} {
		meta, err := readSessionMeta(id)
		require.NoError(t, err)
		assert.Nil(t, meta.CompletedAt, "a 404 can mean no token was found, so the session is kept")
	}

	env.fake.projects["gone/project"] = 7
	_, err = env.exec("--all")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"sess-good", "sess-1", "sess-2"}, env.fake.sessionsCreated, "the sessions sync once the project is visible")
	assert.Empty(t, readStatus(t).ProjectNotFound)
}

func TestSyncAll_RecordsHostsWithoutToken(t *testing.T) {
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "JOB_TOKEN", "CI_JOB_TOKEN", "GLAB_ENABLE_CI_AUTOLOGIN"} {
		t.Setenv(name, "")
	}
	env := newSyncAllEnvWithConfig(t, true, "hosts:\n  "+testHost+":\n    api_protocol: https\n")
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	env.writeClaudeSession(t, "sess-2", "gone/project", "toolu_2")

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	assert.Equal(t, []string{testHost + "/gone/project"}, status.ProjectNotFound, "the host is recorded because this lookup failed")
	assert.Equal(t, []string{testHost}, status.NoToken, "each host is recorded once")
}

func TestSyncAll_HostWithoutTokenThatSyncsIsNotRecorded(t *testing.T) {
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "JOB_TOKEN", "CI_JOB_TOKEN", "GLAB_ENABLE_CI_AUTOLOGIN"} {
		t.Setenv(name, "")
	}
	env := newSyncAllEnvWithConfig(t, true, "hosts:\n  "+testHost+":\n    api_protocol: https\n")
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	assert.Equal(t, 1, status.SessionsSynced)
	assert.Empty(t, status.NoToken, "nothing failed, so the missing token did not matter")
}

func TestSyncAll_HostWithTokenIsNotRecorded(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, readStatus(t).NoToken)
}

func TestSyncAll_HostWithJobTokenIsNotRecorded(t *testing.T) {
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "JOB_TOKEN", "CI_JOB_TOKEN", "GLAB_ENABLE_CI_AUTOLOGIN"} {
		t.Setenv(name, "")
	}
	env := newSyncAllEnvWithConfig(t, true, "hosts:\n  "+testHost+":\n    api_protocol: https\n    job_token: job-token\n")
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, readStatus(t).NoToken)
}

func TestSyncAll_RejectedSessionErrorIsNotRepeated(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.sessionStatus = http.StatusForbidden
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	require.Len(t, status.Errors, 1)
	assert.Equal(t, 1, strings.Count(status.Errors[0], "could not create session"), status.Errors[0])
}

func TestSyncAll_PostsLargeSessionsInBatches(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", toolIDs(1201)...)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Zero(t, env.fake.auditRejections, "every batch is within GitLab's limits")
	// Each of these events is 19 JSON elements, so 236 fit in maxElementsPerRequest.
	assert.Equal(t, []int{236, 236, 236, 236, 236, 21}, env.fake.eventBatches)
	assert.Equal(t, 1201, env.fake.eventsPosted)
	assertCursorAtEnd(t, path)
}

func TestSyncAll_SplitsBatchesOverGitLabLimits(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.maxElements = 1000
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", toolIDs(300)...)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.NotZero(t, env.fake.auditRejections)
	assert.Equal(t, 300, env.fake.eventsPosted)
	assertCursorAtEnd(t, path)
}

func TestSyncAll_SkipsEventTooLargeForGitLab(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.tooLargeToolCall = "toolu_7"
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", toolIDs(20)...)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 19, env.fake.eventsPosted)
	status := readStatus(t)
	assert.Equal(t, 19, status.EventsPosted)
	assert.Equal(t, 1, status.EventsTooLarge)
	assert.Empty(t, status.Errors)
	assertCursorAtEnd(t, path)
}

func TestSyncAll_KeepsEventsGitLabRejectsForAnotherReason(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.auditStatus = http.StatusBadRequest
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", toolIDs(20)...)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 1, env.fake.auditRequests, "the batch is not split")
	status := readStatus(t)
	assert.Zero(t, status.EventsTooLarge)
	require.Len(t, status.Errors, 1)
	assertCursorAt(t, "sess-1", 0)
}

func TestSyncAll_WaitsOutRateLimit(t *testing.T) {
	env := newSyncAllEnv(t, true)
	// More than the API client retries on its own, with Retry-After: 0.
	env.fake.rateLimited = 7
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", toolIDs(3)...)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 8, env.fake.auditRequests)
	assert.Equal(t, 3, env.fake.eventsPosted)
	assert.Empty(t, readStatus(t).Errors)
	assertCursorAtEnd(t, path)
}

func TestSyncAll_RetriesWritesOnServerError(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.badGateway = map[string]int{"session": 1, "audit": 1, "complete": 1}
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	idle := time.Now().Add(-idleCompleteAfter - time.Hour)
	require.NoError(t, os.Chtimes(path, idle, idle))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, readStatus(t).Errors)
	assert.Len(t, env.fake.sessionsCreated, 1)
	assert.Equal(t, 1, env.fake.eventsPosted)
	assert.Equal(t, 1, env.fake.completed)
}

func TestSyncAll_FailedAuditEventsErrorIsNotRepeated(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.auditStatus = http.StatusForbidden
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	require.Len(t, status.Errors, 1)
	assert.Equal(t, 1, strings.Count(status.Errors[0], "could not post audit events"), status.Errors[0])
}

func TestSyncAll_SkipsSessionsWithNothingNew(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, writeCursor("sess-1", info.Size()))

	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.projectLookups, "a session with nothing new must not reach the API")
	assert.Empty(t, env.fake.sessionsCreated)
	assert.Zero(t, readStatus(t).SessionsSynced)
}

func TestSyncAll_LooksUpEachProjectOnce(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	env.writeClaudeSession(t, "sess-2", "my-group/my-project", "toolu_2")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"sess-1", "sess-2"}, env.fake.sessionsCreated)
	assert.Equal(t, 1, env.fake.projectLookups["my-group/my-project"])
	assert.Equal(t, 2, readStatus(t).SessionsSynced)
}

func TestSyncAll_ContinuesPastFailedProjectLookup(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projectStatus = http.StatusForbidden
	env.writeClaudeSession(t, "sess-bad", "gone/project", "toolu_1")
	env.writeClaudeSession(t, "sess-good", "my-group/my-project", "toolu_2")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"sess-good"}, env.fake.sessionsCreated)
	cursor, err := readCursor("sess-bad")
	require.NoError(t, err)
	assert.Zero(t, cursor, "a failed session keeps its cursor so the next run retries it")

	status := readStatus(t)
	assert.Equal(t, 1, status.SessionsSynced)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], "gone/project")
	assert.Equal(t, 1, env.fake.projectLookups["gone/project"])
}

func TestSyncAll_CompletesIdleSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	idle := time.Now().Add(-idleCompleteAfter - time.Hour)
	require.NoError(t, os.Chtimes(path, idle, idle))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 1, env.fake.completed)
	meta, err := readSessionMeta("sess-1")
	require.NoError(t, err)
	assert.NotNil(t, meta.CompletedAt)
	assert.Equal(t, 1, readStatus(t).SessionsCompleted)

	// A completed session is not synced again.
	_, err = env.exec("--all")
	require.NoError(t, err)
	assert.Equal(t, 1, env.fake.completed)
}

func TestSyncAll_DoesNotCompleteActiveSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Zero(t, env.fake.completed)
	meta, err := readSessionMeta("sess-1")
	require.NoError(t, err)
	assert.Nil(t, meta.CompletedAt)
}

func TestSyncAll_ForgetsSessionWithDeletedTranscript(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	require.NoError(t, writeCursor("sess-1", 10))
	require.NoError(t, os.Remove(path))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.projectLookups)
	assert.Empty(t, readStatus(t).Errors, "a deleted transcript is not an error")
	_, err = readSessionMeta("sess-1")
	require.ErrorIs(t, err, errNoSessionMeta)
	cp, err := cursorPath("sess-1")
	require.NoError(t, err)
	assert.NoFileExists(t, cp)
}

func TestSyncAll_RecordsUnreadableCursorsDir(t *testing.T) {
	env := newSyncAllEnv(t, true)
	gaigDir := filepath.Join(env.home, "glab", "gaig")
	require.NoError(t, os.MkdirAll(gaigDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gaigDir, "cursors"), nil, 0o644))

	_, err := env.exec("--all")
	require.Error(t, err)

	status := readStatus(t)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], "could not read cursors directory")
}

func TestSyncAll_FindsTranscriptAfterDirectoryChange(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	meta, err := readSessionMeta("sess-1")
	require.NoError(t, err)
	meta.Transcript = filepath.Join(env.home, ".claude", "projects", "-elsewhere", "sess-1.jsonl")
	require.NoError(t, writeSessionMeta("sess-1", *meta))

	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 1, env.fake.eventsPosted)
	meta, err = readSessionMeta("sess-1")
	require.NoError(t, err)
	assert.Equal(t, path, meta.Transcript, "the found path is recorded so later runs don't search again")
}

func TestSyncAll_ForgetsCompletedSessionOnceTranscriptDeleted(t *testing.T) {
	env := newSyncAllEnv(t, true)
	deleted := env.writeClaudeSession(t, "sess-deleted", "my-group/my-project", "toolu_1")
	env.writeClaudeSession(t, "sess-kept", "my-group/my-project", "toolu_2")
	require.NoError(t, markSessionCompleted("sess-deleted", time.Now()))
	require.NoError(t, markSessionCompleted("sess-kept", time.Now()))
	require.NoError(t, os.Remove(deleted))

	_, err := env.exec("--all")
	require.NoError(t, err)

	_, err = readSessionMeta("sess-deleted")
	require.ErrorIs(t, err, errNoSessionMeta)
	_, err = readSessionMeta("sess-kept")
	require.NoError(t, err, "a completed session can still be resumed while its transcript exists")
	assert.Empty(t, env.fake.sessionsCreated, "completed sessions make no API calls")
	assert.Empty(t, readStatus(t).Errors)
}

func TestSyncAll_KeepsCompletedOpenCodeSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, writeSessionMeta("ses_1", sessionMeta{
		AgentType:         "opencode",
		PathWithNamespace: "my-group/my-project",
		Host:              testHost,
		Transcript:        "/opencode/that/was/removed",
	}))
	require.NoError(t, markSessionCompleted("ses_1", time.Now()))

	// The executor has no expectations, so running opencode export fails the test.
	_, err := env.exec("--all")
	require.NoError(t, err)

	meta, err := readSessionMeta("ses_1")
	require.NoError(t, err)
	assert.Equal(t, "/opencode/that/was/removed", meta.Transcript)
}

func installOpenCodePlugin(t *testing.T) {
	t.Helper()
	path, err := opencodeplugin.Path()
	require.NoError(t, err)
	_, err = opencodeplugin.Install(path)
	require.NoError(t, err)
}

func TestSyncAll_HooksRemovedPausesOnlyClaudeCode(t *testing.T) {
	env := newSyncAllEnv(t, false)
	installOpenCodePlugin(t)
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	env.writeOpenCodeSession(t, "ses_1", "call_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"ses_1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"claude-code: the glab Stop hook is not installed in ~/.claude/settings.json"}, readStatus(t).Paused)
}

func TestSyncAll_PluginRemovedPausesOnlyOpenCode(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path, err := opencodeplugin.Path()
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	require.NoError(t, writeSessionMeta("ses_1", sessionMeta{
		AgentType:         "opencode",
		PathWithNamespace: "my-group/my-project",
		Host:              testHost,
		Transcript:        "/bin/sh",
	}))

	// The executor has no expectations, so running opencode export fails the test.
	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"sess-1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"opencode: the glab OpenCode plugin is not installed in ~/.config/opencode/plugins/glab-govern.js"}, readStatus(t).Paused)
}

func TestOpenCodePausedReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	path, err := opencodeplugin.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	for name, content := range map[string]string{
		"out of date":           "// Installed by glab govern setup.\n// an older version\n",
		"not installed by glab": "// the user's own plugin\n",
	} {
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		reason, err := openCode{}.pausedReason()
		require.NoError(t, err)
		if name == "out of date" {
			assert.Empty(t, reason, "an older plugin still syncs")
		} else {
			assert.Equal(t, "the glab OpenCode plugin is not installed in ~/.config/opencode/plugins/glab-govern.js", reason)
		}
	}
}

func TestOpenCodePausedReason_Windows(t *testing.T) {
	reason, err := openCodePausedReason("windows")
	require.NoError(t, err)
	assert.Equal(t, "the OpenCode plugin is not supported on Windows", reason)
}

func TestSyncAll_UnsupportedAgent(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, writeSessionMeta("sess-1", sessionMeta{AgentType: "other-agent", PathWithNamespace: "my-group/my-project", Host: testHost}))

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], `unsupported agent "other-agent"`)
}

func TestSyncAll_FlagConflicts(t *testing.T) {
	env := newSyncAllEnv(t, true)

	_, err := env.exec("--all -R my-group/my-project")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")

	_, err = env.exec("--all --complete")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

// writeOpenCodeSession records an OpenCode session whose export contains one
// tool call per call ID.
func (e *syncAllEnv) writeOpenCodeSession(t *testing.T, sessionID string, callIDs ...string) {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "opencode")
	require.NoError(t, os.WriteFile(binary, nil, 0o755))
	require.NoError(t, writeSessionMeta(sessionID, sessionMeta{
		AgentType:         "opencode",
		PathWithNamespace: "my-group/my-project",
		Host:              testHost,
		Transcript:        binary,
	}))
	export := openCodeExportJSON(t, time.Now(), callIDs...)
	e.mExec.EXPECT().
		ExecWithIO(gomock.Any(), binary, []string{"session", "export", "--standalone", sessionID}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
			_, err := stdout.Write(export)
			return err
		})
}

func TestSyncAll_UploadsOpenCodeSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.writeOpenCodeSession(t, "ses_1", "call_1", "call_2")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"ses_1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{eventToolInvoked, eventToolInvoked, eventToolResponse, eventToolResponse}, eventNames(env.fake.events),
		"completed OpenCode tool calls also record their result")
	cursor, err := readCursor("ses_1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), cursor)
}

func TestRunSync_GovernanceNotEnabled(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.identityStatus = http.StatusNotFound
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")

	out, err := env.exec("-R my-group/my-project")
	require.NoError(t, err)

	assert.Contains(t, out.String(), governanceNotEnabled)
	assert.Empty(t, out.Stderr(), "governance being off is not a failure")
}

func TestRunSync_GovernanceNotEnabledSilent(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.identityStatus = http.StatusNotFound
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
	writeHookTranscript(t, "sess-1", "toolu_1")

	out, err := env.exec("-R my-group/my-project --silent")
	require.NoError(t, err)

	assert.Empty(t, out.String(), "hooks run with --silent")
	assert.Empty(t, out.Stderr())
	assert.Empty(t, env.fake.sessionsCreated, "no session is created without an identity")
}

// writeHookTranscript writes a Claude Code transcript where a hook run from
// the current directory looks for it.
func writeHookTranscript(t *testing.T, sessionID string, toolIDs ...string) {
	t.Helper()

	cwd, err := currentCWD()
	require.NoError(t, err)
	path, err := transcriptPath(sessionID, cwd)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, claudeTranscriptLines(t, sessionID, toolIDs...), 0o644))
}

// claudeTranscriptLines returns a Claude Code transcript with one Bash tool
// call for each ID.
func claudeTranscriptLines(t *testing.T, sessionID string, toolIDs ...string) []byte {
	t.Helper()

	var content []byte
	for _, id := range toolIDs {
		line, err := json.Marshal(map[string]any{
			"type":      "assistant",
			"sessionId": sessionID,
			"timestamp": time.Now().Format(time.RFC3339),
			"message": map[string]any{
				"content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash"}},
			},
		})
		require.NoError(t, err)
		content = append(append(content, line...), '\n')
	}
	return content
}

func TestRunSync_IdentityErrorStillWarns(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.identityStatus = http.StatusForbidden
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")

	out, err := env.exec("-R my-group/my-project")
	require.NoError(t, err)

	assert.Contains(t, out.Stderr(), "warning: ")
	assert.NotContains(t, out.String(), governanceNotEnabled, "only a 404 means governance is off")
}

func TestRunSync_RecordsMetaBeforeProjectLookup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-meta")
	t.Setenv("OPENCODE_SESSION_ID", "")

	ctrl := gomock.NewController(t)
	tc := gitlabtesting.NewTestClientWithCtrl(ctrl, gitlab.WithBaseURL("https://"+testHost))
	tc.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("project not found"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "test-token", testHost, api.WithGitLabClient(tc.Client))),
	)

	_, err := exec("--silent -R my-group/my-project")
	require.Error(t, err)

	meta, err := readSessionMeta("sess-meta")
	require.NoError(t, err)
	assert.Equal(t, "claude-code", meta.AgentType)
	assert.Equal(t, "my-group/my-project", meta.PathWithNamespace)
	assert.Equal(t, "gitlab.com", meta.Host)
	assert.Equal(t, "sess-meta.jsonl", filepath.Base(meta.Transcript))
}

// writeUnfoundOpenCodeSession records an OpenCode session that opencode export
// reports as not found, last recorded by a hook at recorded.
func (e *syncAllEnv) writeUnfoundOpenCodeSession(t *testing.T, sessionID string, recorded time.Time) {
	t.Helper()

	require.NoError(t, writeSessionMeta(sessionID, sessionMeta{
		AgentType:         "opencode",
		PathWithNamespace: "my-group/my-project",
		Host:              testHost,
		Transcript:        "/bin/sh",
	}))
	mp, err := metaPath(sessionID)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(mp, recorded, recorded))
	e.mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/bin/sh", []string{"session", "export", "--standalone", sessionID}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, _, stderr io.Writer) error {
			_, _ = io.WriteString(stderr, "Error: Session not found: "+sessionID)
			return errors.New("exit status 1")
		})
}

func TestSyncAll_KeepsRecentOpenCodeSessionNotFound(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.writeUnfoundOpenCodeSession(t, "ses_1", time.Now())

	_, err := env.exec("--all")
	require.NoError(t, err)

	_, err = readSessionMeta("ses_1")
	require.NoError(t, err, "a recently recorded session must not be forgotten")
	status := readStatus(t)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], "the agent reports no such session")
}

func TestSyncAll_ForgetsLongUnfoundOpenCodeSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.writeUnfoundOpenCodeSession(t, "ses_1", time.Now().Add(-forgetUnfoundAfter-time.Hour))

	_, err := env.exec("--all")
	require.NoError(t, err)

	_, err = readSessionMeta("ses_1")
	require.ErrorIs(t, err, errNoSessionMeta)
	assert.Empty(t, readStatus(t).Errors)
}

func TestSyncAll_ForgetsLongCompletedOpenCodeSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, writeSessionMeta("ses_old", sessionMeta{
		AgentType:         "opencode",
		PathWithNamespace: "my-group/my-project",
		Host:              testHost,
		Transcript:        "/opencode/that/was/removed",
	}))
	require.NoError(t, markSessionCompleted("ses_old", time.Now().Add(-forgetUnfoundAfter-time.Hour)))

	// The executor has no expectations: forgetting must not run opencode export.
	_, err := env.exec("--all")
	require.NoError(t, err)

	_, err = readSessionMeta("ses_old")
	require.ErrorIs(t, err, errNoSessionMeta)
	cp, err := cursorPath("ses_old")
	require.NoError(t, err)
	assert.NoFileExists(t, cp)
}

func TestOpenCode_RecordedFallsBackToPATH(t *testing.T) {
	t.Parallel()

	binary := func(t *testing.T, recorded string) string {
		t.Helper()
		src, _, err := openCode{}.recorded("ses_1", recorded)
		require.NoError(t, err)
		oc, ok := src.(*openCodeTranscript)
		require.True(t, ok)
		return oc.binary
	}

	assert.Empty(t, binary(t, "/opencode/that/was/removed"), "a missing recorded binary is looked up in PATH instead")
	assert.Equal(t, "/bin/sh", binary(t, "/bin/sh"))
}

func TestOpenCodeTranscript_LastActivityWithoutUpdated(t *testing.T) {
	t.Parallel()

	newTranscript := func(export string) *openCodeTranscript {
		src := &openCodeTranscript{sessionID: "ses_1"}
		src.raw = []byte(export)
		src.export = &openCodeExport{}
		require.NoError(t, json.Unmarshal(src.raw, src.export))
		return src
	}

	t.Run("falls back to the latest message", func(t *testing.T) {
		t.Parallel()
		src := newTranscript(`{"info":{"time":{"created":1000}},"messages":[{"info":{"role":"user","time":{"created":5000}},"parts":[]}]}`)
		last, err := src.lastActivity(t.Context())
		require.NoError(t, err)
		assert.Equal(t, time.UnixMilli(5000), last)
	})

	t.Run("treats a session without timestamps as active", func(t *testing.T) {
		t.Parallel()
		src := newTranscript(`{"info":{},"messages":[]}`)
		last, err := src.lastActivity(t.Context())
		require.NoError(t, err)
		assert.Less(t, time.Since(last), idleCompleteAfter)
	})
}

func TestRunSync_RecordsOpenCodeMeta(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("OPENCODE_SESSION_ID", "ses_meta")

	ctrl := gomock.NewController(t)
	tc := gitlabtesting.NewTestClientWithCtrl(ctrl, gitlab.WithBaseURL("https://"+testHost))
	tc.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("project not found"))
	mExec := cmdtest.NewMockExecutor(ctrl)
	mExec.EXPECT().LookPath("opencode").Return("/home/me/.opencode/bin/opencode", nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "test-token", testHost, api.WithGitLabClient(tc.Client))),
		cmdtest.WithExecutor(mExec),
	)

	_, err := exec("--silent -R my-group/my-project")
	require.Error(t, err)

	meta, err := readSessionMeta("ses_meta")
	require.NoError(t, err)
	assert.Equal(t, "opencode", meta.AgentType)
	assert.Equal(t, "/home/me/.opencode/bin/opencode", meta.Transcript)
}

func openCodeExportJSON(t *testing.T, updated time.Time, callIDs ...string) []byte {
	t.Helper()

	parts := []map[string]any{}
	for _, id := range callIDs {
		parts = append(parts, map[string]any{
			"id":     "prt_" + id,
			"type":   "tool",
			"callID": id,
			"tool":   "bash",
			"state":  map[string]any{"status": "completed", "time": map[string]any{"start": updated.UnixMilli()}},
		})
	}
	data, err := json.Marshal(map[string]any{
		"info": map[string]any{
			"id":   "ses_1",
			"time": map[string]any{"created": updated.Add(-time.Hour).UnixMilli(), "updated": updated.UnixMilli()},
		},
		"messages": []map[string]any{
			{
				"info":  map[string]any{"role": "user", "time": map[string]any{"created": updated.Add(-time.Hour).UnixMilli()}},
				"parts": []map[string]any{{"id": "prt_goal", "type": "text", "text": "fix the flaky test"}},
			},
			{
				"info":  map[string]any{"role": "assistant", "time": map[string]any{"created": updated.UnixMilli()}},
				"parts": parts,
			},
		},
	})
	require.NoError(t, err)
	return data
}

func TestOpenCodeTranscript_Read(t *testing.T) {
	t.Parallel()

	updated := time.UnixMilli(time.Now().UnixMilli())
	export := openCodeExportJSON(t, updated, "call_1", "call_2", "call_3")

	ctrl := gomock.NewController(t)
	mExec := cmdtest.NewMockExecutor(ctrl)
	mExec.EXPECT().LookPath("opencode").Return("/usr/local/bin/opencode", nil)
	// OpenCode 1 has no session export, so it prints its help and fails.
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/usr/local/bin/opencode", []string{"session", "export", "--standalone", "ses_1"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, _, stderr io.Writer) error {
			_, _ = io.WriteString(stderr, "opencode session\n\nmanage sessions\n")
			return errors.New("exit status 1")
		}).
		Times(1)
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/usr/local/bin/opencode", []string{"export", "ses_1"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, stderr io.Writer) error {
			_, _ = io.WriteString(stderr, "Exporting session: ses_1\n")
			_, err := stdout.Write(export)
			return err
		}).
		Times(1)

	src := &openCodeTranscript{sessionID: "ses_1", executor: mExec}

	data, cursor, err := src.read(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, "fix the flaky test", data.Goal)
	require.Len(t, data.ToolCalls, 3)
	assert.Equal(t, "call_1", data.ToolCalls[0].ID)
	assert.Equal(t, "bash", data.ToolCalls[0].Name)
	assert.Equal(t, updated, data.ToolCalls[0].Timestamp)
	assert.Equal(t, int64(3), cursor)

	data, cursor, err = src.read(t.Context(), 2)
	require.NoError(t, err)
	assert.Empty(t, data.Goal)
	require.Len(t, data.ToolCalls, 1)
	assert.Equal(t, "call_3", data.ToolCalls[0].ID)
	assert.Equal(t, int64(3), cursor)

	last, err := src.lastActivity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, updated, last)

	sum, err := src.checksum(t.Context())
	require.NoError(t, err)
	assert.Len(t, sum, 64)
}

func TestOpenCodeTranscript_ExportFails(t *testing.T) {
	t.Parallel()

	export := func(t *testing.T, stderrText string, attempts int) error {
		t.Helper()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().
			ExecWithIO(gomock.Any(), "/bin/opencode", gomock.Any(), nil, nil, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, _, stderr io.Writer) error {
				_, _ = io.WriteString(stderr, stderrText)
				return errors.New("exit status 1")
			}).
			Times(attempts)

		src := &openCodeTranscript{sessionID: "ses_1", binary: "/bin/opencode", executor: mExec}
		_, cursor, err := src.read(t.Context(), 5)
		assert.Equal(t, int64(5), cursor)
		return err
	}

	t.Run("session not found", func(t *testing.T) {
		t.Parallel()
		err := export(t, "Error: Session not found: ses_1", 1)
		require.ErrorIs(t, err, errAgentSessionNotFound)
		require.NotErrorIs(t, err, errTranscriptNotFound, "the job may see a different OpenCode data directory, so this is not proof of deletion")
	})

	t.Run("other failure", func(t *testing.T) {
		t.Parallel()
		err := export(t, "Error: database is locked", len(openCodeExportCommands))
		require.Error(t, err)
		require.NotErrorIs(t, err, errTranscriptNotFound, "only a deleted session may make --all forget it")
		assert.Contains(t, err.Error(), "database is locked")
	})
}

func TestClaudeTranscript_Missing(t *testing.T) {
	t.Parallel()

	src := claudeTranscript{path: filepath.Join(t.TempDir(), "missing.jsonl")}
	_, _, err := src.read(t.Context(), 0)
	require.ErrorIs(t, err, errTranscriptNotFound)
}

func TestGoalFromText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "fix the bug", goalFromText("fix the bug"))
	assert.Empty(t, goalFromText("<bash-input>ls</bash-input>"))
	assert.Equal(t, strings.Repeat("a", 256)+"...", goalFromText(strings.Repeat("a", 300)))
	assert.Empty(t, goalFromText(string(bytes.Repeat([]byte(" "), 2))+"<tag>"))
}

// processExecutor runs commands the same way as the production executor in
// internal/cmdutils, so tests can observe real process and pipe behavior.
type processExecutor struct {
	cmdutils.Executor
}

func (processExecutor) ExecWithIO(ctx context.Context, name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := osexec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// fakeOpenCode writes a shell script that stands in for opencode.
func fakeOpenCode(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "opencode")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	return path
}

// openCodeV2ExportJSON returns an OpenCode 2 session export, shaped like a
// real one: a skipped first prompt, a completed shell call, a failed read, a
// call aborted before it ran, a call still streaming, and a block glab does not
// know.
func openCodeV2ExportJSON(t *testing.T, created time.Time) []byte {
	t.Helper()

	ms := func(d time.Duration) int64 { return created.Add(d).UnixMilli() }
	data, err := json.Marshal(map[string]any{
		"info": map[string]any{
			"id":       "ses_2",
			"location": map[string]any{"directory": "/home/me/project"},
			"time":     map[string]any{"created": ms(0), "updated": ms(10 * time.Second), "idle": ms(10 * time.Second)},
		},
		"messages": []map[string]any{
			{"id": "msg_0", "type": "user", "time": map[string]any{"created": ms(0)}, "text": "<command>/init</command>"},
			{"id": "msg_1", "type": "user", "time": map[string]any{"created": ms(0)}, "text": "explain each file", "files": []any{}, "agents": []any{}},
			{
				"id": "msg_2", "type": "assistant", "time": map[string]any{"created": ms(time.Second), "completed": ms(5 * time.Second)},
				"content": []map[string]any{
					{"type": "text", "text": "I'll look at the files."},
					{
						"type": "tool", "id": "call_ls", "name": "shell",
						"state": map[string]any{"status": "completed", "input": map[string]any{"command": "ls -la"}, "content": []any{map[string]any{"type": "text"}}},
						"time":  map[string]any{"created": ms(2 * time.Second), "ran": ms(2*time.Second + 100*time.Millisecond), "completed": ms(2*time.Second + 300*time.Millisecond)},
					},
					{
						"type": "tool", "id": "call_read", "name": "read",
						"state": map[string]any{"status": "error", "input": map[string]any{"path": "missing.md"}, "error": map[string]any{"type": "unknown", "message": "File not found"}},
						"time":  map[string]any{"created": ms(3 * time.Second), "ran": ms(3 * time.Second), "completed": ms(3*time.Second + 50*time.Millisecond)},
					},
					{"type": "future-block", "content": "a shape glab does not know"},
					{
						"type": "tool", "id": "call_edit", "name": "edit",
						"state": map[string]any{"status": "error", "input": map[string]any{}, "error": map[string]any{"type": "aborted"}},
						"time":  map[string]any{"created": ms(4 * time.Second)},
					},
					{
						"type": "tool", "id": "call_write", "name": "write",
						"state": map[string]any{"status": "streaming", "input": `{"path": "no`},
						"time":  map[string]any{"created": ms(5 * time.Second)},
					},
				},
			},
			{"id": "msg_4", "type": "idle", "time": map[string]any{"created": ms(9 * time.Second)}},
			{"id": "msg_3", "type": "outcome", "outcome": map[string]any{"type": "completed"}, "time": map[string]any{"created": ms(10 * time.Second)}},
		},
	})
	require.NoError(t, err)
	return data
}

func TestOpenCodeTranscript_ReadOpenCode2(t *testing.T) {
	t.Parallel()

	created := time.UnixMilli(time.Now().Add(-time.Hour).UnixMilli())
	export := openCodeV2ExportJSON(t, created)
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/opt/opencode", []string{"session", "export", "--standalone", "ses_2"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
			_, err := stdout.Write(export)
			return err
		}).
		Times(1)
	src := &openCodeTranscript{sessionID: "ses_2", binary: "/opt/opencode", executor: mExec}

	data, cursor, err := src.read(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, "explain each file", data.Goal, "a command prompt is not the goal")
	assert.Equal(t, created, data.StartedAt)
	assert.Equal(t, int64(4), cursor)

	require.Len(t, data.ToolCalls, 4)
	assert.Equal(t, "call_ls", data.ToolCalls[0].ID)
	assert.Equal(t, "shell", data.ToolCalls[0].Name)
	assert.JSONEq(t, `{"command":"ls -la"}`, string(data.ToolCalls[0].Input))
	assert.Equal(t, created.Add(2*time.Second+100*time.Millisecond), data.ToolCalls[0].Timestamp, "a call starts when it ran")
	assert.Equal(t, created.Add(3*time.Second), data.ToolCalls[1].Timestamp)
	assert.Equal(t, created.Add(4*time.Second), data.ToolCalls[2].Timestamp, "a call that never ran starts when it was created")
	assert.Equal(t, "call_write", data.ToolCalls[3].ID)
	assert.Nil(t, data.ToolCalls[3].Input, "a streaming call's partial input is not recorded")

	require.Len(t, data.ToolResults, 3, "a streaming call has no result yet")
	assert.False(t, data.ToolResults[0].IsError)
	assert.Equal(t, 200*time.Millisecond, data.ToolResults[0].Duration)
	assert.True(t, data.ToolResults[1].IsError)
	assert.Equal(t, "File not found", data.ToolResults[1].Error)
	assert.Equal(t, 50*time.Millisecond, data.ToolResults[1].Duration)
	assert.Equal(t, "aborted", data.ToolResults[2].Error, "an error without a message reports its type")

	data, cursor, err = src.read(t.Context(), 1)
	require.NoError(t, err)
	assert.Empty(t, data.Goal)
	require.Len(t, data.ToolCalls, 3)
	assert.Equal(t, "call_read", data.ToolCalls[0].ID)
	assert.Equal(t, int64(4), cursor)

	last, err := src.lastActivity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, created.Add(10*time.Second), last)
}

func TestOpenCodeTranscript_SessionNotFoundStopsFallback(t *testing.T) {
	t.Parallel()

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/opt/opencode", []string{"session", "export", "--standalone", "ses_gone"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, _, stderr io.Writer) error {
			_, _ = io.WriteString(stderr, "Session not found: ses_gone\n")
			return errors.New("exit status 1")
		}).
		Times(1)
	src := &openCodeTranscript{sessionID: "ses_gone", binary: "/opt/opencode", executor: mExec}

	_, _, err := src.read(t.Context(), 0)
	require.ErrorIs(t, err, errAgentSessionNotFound)
}

func TestOpenCodeTranscript_ReportsBothExportFailures(t *testing.T) {
	t.Parallel()

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/opt/opencode", gomock.Any(), nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, args []string, _ []string, _ io.Reader, _, stderr io.Writer) error {
			_, _ = io.WriteString(stderr, "broken "+args[0])
			return errors.New("exit status 2")
		}).
		Times(2)
	src := &openCodeTranscript{sessionID: "ses_1", binary: "/opt/opencode", executor: mExec}

	_, _, err := src.read(t.Context(), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "opencode session export --standalone ses_1: exit status 2: broken session")
	assert.Contains(t, err.Error(), "opencode export ses_1: exit status 2: broken export")
}

func TestOpenCodeTranscript_ChildKeepingOutputOpen(t *testing.T) {
	t.Parallel()

	// The background sleep inherits stdout and outlives opencode. Reading
	// stdout through a pipe would block until the sleep exits.
	binary := fakeOpenCode(t, `sleep 20 &
printf '%s' '{"info":{"time":{"created":1000,"updated":2000}},"messages":[]}'`)
	src := &openCodeTranscript{sessionID: "ses_1", binary: binary, executor: processExecutor{}}

	start := time.Now()
	last, err := src.lastActivity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, time.UnixMilli(2000), last)
	assert.Less(t, time.Since(start), 5*time.Second, "export must not wait for opencode's child process")
}

func TestOpenCodeTranscript_ExportTimesOut(t *testing.T) {
	t.Parallel()

	binary := fakeOpenCode(t, `sleep 20 &
sleep 20`)
	src := &openCodeTranscript{sessionID: "ses_1", binary: binary, executor: processExecutor{}, timeout: 300 * time.Millisecond}

	start := time.Now()
	_, _, err := src.read(t.Context(), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 300ms")
	assert.Less(t, time.Since(start), 5*time.Second, "a hung export must not block the run")
}

func TestOpenCodeTranscript_TimeoutStopsFallback(t *testing.T) {
	t.Parallel()

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/opt/opencode", gomock.Any(), nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, _, _ []string, _ io.Reader, _, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		}).
		Times(1)
	src := &openCodeTranscript{sessionID: "ses_1", binary: "/opt/opencode", executor: mExec, timeout: 50 * time.Millisecond}

	_, _, err := src.read(t.Context(), 0)
	require.ErrorIs(t, err, errOpenCodeExportTimedOut)
}

// A hook must finish quickly, so unlike a sync run directly it does not wait
// out GitLab's rate limit.
func TestSyncSession_ByCaller(t *testing.T) {
	tests := []struct {
		name       string
		silent     bool
		want       string
		wantPosted int
	}{
		{name: "hook", silent: true, want: "hook", wantPosted: 0},
		{name: "run directly", silent: false, want: "manual", wantPosted: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("GLAB_CONFIG_DIR", dir)

			fake := &fakeGitLab{projects: map[string]int64{}, projectLookups: map[string]int{}, rateLimited: 7}
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)
			gl, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(server.URL+"/api/v4"))
			require.NoError(t, err)
			client := cmdtest.NewTestApiClient(t, nil, "test-token", testHost, api.WithGitLabClient(gl))

			line, err := json.Marshal(map[string]any{
				"type":      "assistant",
				"sessionId": "sess-1",
				"timestamp": time.Now().Format(time.RFC3339),
				"message": map[string]any{
					"content": []map[string]any{{"type": "tool_use", "id": "toolu_1", "name": "Bash"}},
				},
			})
			require.NoError(t, err)
			path := filepath.Join(dir, "sess-1.jsonl")
			require.NoError(t, os.WriteFile(path, append(line, '\n'), 0o644))

			ios, _, _, _ := cmdtest.TestIOStreams()
			opts := &options{io: ios, silent: tt.silent, agentType: "claude-code"}
			recordWaits(opts)
			dest := target{client: client, project: &gitlab.Project{ID: 42}}
			require.NoError(t, syncSession(t.Context(), dest, "sess-1", claudeTranscript{path: path}, opts))

			assert.Equal(t, []string{tt.want}, fake.syncTypes)
			assert.Equal(t, tt.wantPosted, fake.eventsPosted)
		})
	}
}

func TestAuditEventPoster_RateLimit(t *testing.T) {
	tests := []struct {
		name       string
		opts       options
		limited    int
		retryAfter string
		wantErr    bool
		wantWaits  int
	}{
		{name: "waits", opts: options{all: true}, limited: 2, wantWaits: 2},
		{name: "gives up after the most waits", opts: options{all: true}, limited: 100, wantErr: true, wantWaits: maxRateLimitWaits},
		{
			name:    "gives up when the run has waited enough",
			opts:    options{all: true, rateLimitWaited: maxRateLimitWaitPerRun - time.Second},
			limited: 1, retryAfter: "60", wantErr: true,
		},
		{name: "hook does not wait", opts: options{silent: true}, limited: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			waits := recordWaits(&tt.opts)
			fake := &fakeGitLab{rateLimited: tt.limited, retryAfter: tt.retryAfter}
			poster := newTestPoster(t, fake, &tt.opts)
			_, err := poster.post(t.Context(), []auditEventRequest{{EventName: eventToolInvoked}})

			if tt.wantErr {
				assert.Equal(t, http.StatusTooManyRequests, gitlab.StatusCode(err))
			} else {
				require.NoError(t, err)
			}
			assert.Len(t, *waits, tt.wantWaits)
		})
	}
}

func TestAuditEventPoster_SplitsOnlyOutsideHooks(t *testing.T) {
	tests := []struct {
		name              string
		opts              options
		fallbackInstalled bool
		wantErr           bool
		wantRequests      int
	}{
		{name: "fallback sync splits", opts: options{all: true}, fallbackInstalled: true, wantRequests: 3},
		{name: "manual sync splits", fallbackInstalled: true, wantRequests: 3},
		{name: "hook leaves the batch for the fallback sync", opts: options{silent: true}, fallbackInstalled: true, wantErr: true, wantRequests: 1},
		{name: "hook splits without the fallback sync", opts: options{silent: true}, wantRequests: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if tt.fallbackInstalled {
				installFakeFallbackSync(t, home)
			}
			fake := &fakeGitLab{maxElements: 30}
			poster := newTestPoster(t, fake, &tt.opts)

			events := make([]auditEventRequest, 4)
			for i := range events {
				events[i] = auditEventRequest{EventName: eventToolInvoked, CloudEventID: fmt.Sprintf("event-%d", i)}
			}
			tooLarge, err := poster.post(t.Context(), events)

			if tt.wantErr {
				assert.True(t, requestTooLarge(err), err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 4, fake.eventsPosted)
			}
			assert.Zero(t, tooLarge)
			assert.Equal(t, tt.wantRequests, fake.auditRequests)
		})
	}
}

func newTestPoster(t *testing.T, fake *fakeGitLab, opts *options) auditEventPoster {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	// WithoutRetries alone does not stop api.WithSafeRetries' per-request retries.
	gl, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(server.URL+"/api/v4"), gitlab.WithoutRetries(), gitlab.WithCustomRetryMax(0))
	require.NoError(t, err)
	ios, _, _, _ := cmdtest.TestIOStreams()
	opts.io = ios
	client := cmdtest.NewTestApiClient(t, nil, "test-token", testHost, api.WithGitLabClient(gl))
	return auditEventPoster{client: client, projectID: 42, sessionID: 99, opts: opts}
}

func installFakeFallbackSync(t *testing.T, home string) {
	t.Helper()
	units, err := fallbacksync.Units(runtime.GOOS, home, fallbacksync.Job{})
	if errors.Is(err, fallbacksync.ErrUnsupportedOS) {
		t.Skip("the fallback sync is not available on this OS")
	}
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(units[0].Path), 0o755))
	require.NoError(t, os.WriteFile(units[0].Path, nil, 0o644))
}

func TestWriteCursor_DoesNotMoveBack(t *testing.T) {
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())

	require.NoError(t, writeCursor("sess-1", 200))
	require.NoError(t, writeCursor("sess-1", 100))
	assertCursorAt(t, "sess-1", 200)

	require.NoError(t, writeCursor("sess-1", 300))
	assertCursorAt(t, "sess-1", 300)
}

func TestOptionsWait_StopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	assert.ErrorIs(t, (&options{}).wait(ctx, time.Hour), context.Canceled)
}
