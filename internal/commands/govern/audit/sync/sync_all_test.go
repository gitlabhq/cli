//go:build !integration

package sync

import (
	"bytes"
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
	identityRequests int
	eventBatches     []int
	eventsPosted     int
	completed        int
	// noSessionID makes session creation succeed without returning an ID.
	noSessionID bool
	// identityStatus and sessionStatus, when set, answer those endpoints with
	// that HTTP status. GitLab answers 404 while AI agent governance is off
	// for the project.
	identityStatus int
	sessionStatus  int
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
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"404 Project Not Found"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "path_with_namespace": name})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/ai_agent/identities"):
		f.identityRequests++
		if f.identityStatus != 0 {
			writeStatus(w, f.identityStatus)
			return
		}
		_, _ = io.WriteString(w, `{"id": 7}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/ai_agent/sessions"):
		if f.sessionStatus != 0 {
			writeStatus(w, f.sessionStatus)
			return
		}
		var body sessionRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.sessionsCreated = append(f.sessionsCreated, body.IdempotencyKey)
		if f.noSessionID {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_, _ = io.WriteString(w, `{"id": 99}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/ai_agent/audit_events"):
		var body auditEventsRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		// GitLab rejects batches over its limit, as the real endpoint does.
		if len(body.Events) > maxEventsPerRequest {
			writeStatus(w, http.StatusBadRequest)
			return
		}
		f.eventBatches = append(f.eventBatches, len(body.Events))
		f.eventsPosted += len(body.Events)
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodPatch && strings.HasSuffix(path, "/ai_agent/sessions/99"):
		f.completed++
		_, _ = io.WriteString(w, `{}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
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

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(home, "glab"))
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("OPENCODE_SESSION_ID", "")

	if hooksInstalled {
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

	client, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(server.URL+"/api/v4"))
	require.NoError(t, err)
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "test-token", testHost, api.WithGitLabClient(client))),
		cmdtest.WithExecutor(mExec),
	)

	return &syncAllEnv{home: home, fake: fake, exec: exec, mExec: mExec}
}

// writeClaudeSession writes a Claude Code transcript containing one tool call
// per tool ID, and the metadata a hook records for it.
func (e *syncAllEnv) writeClaudeSession(t *testing.T, sessionID, project string, toolIDs ...string) string {
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
	ids := make([]string, 1201)
	for i := range ids {
		ids[i] = fmt.Sprintf("toolu_%d", i)
	}
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", ids...)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []int{500, 500, 201}, env.fake.eventBatches)
	assert.Equal(t, 1201, env.fake.eventsPosted)
	info, err := os.Stat(path)
	require.NoError(t, err)
	cursor, err := readCursor("sess-1")
	require.NoError(t, err)
	assert.Equal(t, info.Size(), cursor, "a session over GitLab's batch limit is not stuck")
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

func TestSyncAll_HooksRemovedPausesOnlyClaudeCode(t *testing.T) {
	env := newSyncAllEnv(t, false)
	env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1")
	env.writeOpenCodeSession(t, "ses_1", "call_1")

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"ses_1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"claude-code: the glab Stop hook is not installed in ~/.claude/settings.json"}, readStatus(t).Paused)
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
		ExecWithIO(gomock.Any(), binary, []string{"export", sessionID}, nil, nil, gomock.Any(), gomock.Any()).
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
	assert.Equal(t, 2, env.fake.eventsPosted)
	cursor, err := readCursor("ses_1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), cursor)
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
		ExecWithIO(gomock.Any(), "/bin/sh", []string{"export", sessionID}, nil, nil, gomock.Any(), gomock.Any()).
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

	export := func(t *testing.T, stderrText string) error {
		t.Helper()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().
			ExecWithIO(gomock.Any(), "/bin/opencode", gomock.Any(), nil, nil, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, _, stderr io.Writer) error {
				_, _ = io.WriteString(stderr, stderrText)
				return errors.New("exit status 1")
			})

		src := &openCodeTranscript{sessionID: "ses_1", binary: "/bin/opencode", executor: mExec}
		_, cursor, err := src.read(t.Context(), 5)
		assert.Equal(t, int64(5), cursor)
		return err
	}

	t.Run("session not found", func(t *testing.T) {
		t.Parallel()
		err := export(t, "Error: Session not found: ses_1")
		require.ErrorIs(t, err, errAgentSessionNotFound)
		require.NotErrorIs(t, err, errTranscriptNotFound, "the job may see a different OpenCode data directory, so this is not proof of deletion")
	})

	t.Run("other failure", func(t *testing.T) {
		t.Parallel()
		err := export(t, "Error: database is locked")
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
