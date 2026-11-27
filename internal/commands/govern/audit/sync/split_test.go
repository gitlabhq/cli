//go:build !integration

package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/syncstate"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// claudeCall is a Bash call in a Claude Code transcript, recorded in folder dir.
type claudeCall struct {
	id  string
	dir string
}

// claudeTranscriptIn returns the Claude Code transcript of session sess-1
// with each call and its result.
func claudeTranscriptIn(t *testing.T, calls ...claudeCall) []byte {
	t.Helper()
	const sessionID = "sess-1"
	var content []byte
	for _, c := range calls {
		for _, entry := range []map[string]any{
			{
				"type": "assistant", "sessionId": sessionID, "cwd": c.dir, "timestamp": time.Now().Format(time.RFC3339),
				"message": map[string]any{"content": []map[string]any{{"type": "tool_use", "id": c.id, "name": "Bash"}}},
			},
			{
				"type": "user", "sessionId": sessionID, "cwd": c.dir, "timestamp": time.Now().Format(time.RFC3339),
				"message": map[string]any{"content": []map[string]any{{"type": "tool_result", "tool_use_id": c.id}}},
			},
		} {
			line, err := json.Marshal(entry)
			require.NoError(t, err)
			content = append(append(content, line...), '\n')
		}
	}
	return content
}

// writeClaudeTranscriptIn writes the Claude Code transcript of session sess-1
// where a hook run from the current folder looks for it, and returns its path.
func writeClaudeTranscriptIn(t *testing.T, calls ...claudeCall) string {
	t.Helper()
	cwd, err := currentCWD()
	require.NoError(t, err)
	path, err := transcriptPath("sess-1", cwd)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, claudeTranscriptIn(t, calls...), 0o644))
	return path
}

func appendToFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, errors.Join(err, f.Close()))
}

// expectRemote makes git report the project as the origin of dir, once.
func (e *syncAllEnv) expectRemote(dir, project string) {
	e.mExec.EXPECT().
		ExecWithIO(gomock.Any(), "git", []string{"-C", dir, "config", "--get", "remote.origin.url"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(writesStdout("git@" + testHost + ":" + project + ".git\n"))
}

// expectTopLevel makes git report top as the top-level folder of dir's
// repository, once.
func (e *syncAllEnv) expectTopLevel(dir, top string) {
	e.mExec.EXPECT().
		ExecWithIO(gomock.Any(), "git", []string{"-C", dir, "rev-parse", "--show-toplevel"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(writesStdout(top + "\n"))
}

// expectNoRepository makes git report that dir is not in a repository, once.
func (e *syncAllEnv) expectNoRepository(dir string) {
	e.mExec.EXPECT().
		ExecWithIO(gomock.Any(), "git", []string{"-C", dir, "config", "--get", "remote.origin.url"}, nil, nil, gomock.Any(), gomock.Any()).
		Return(errors.New("not a git repository"))
}

// postedTo returns the tool call of each event posted to the GitLab session
// with key in the project, as "<event> <tool call>".
func (f *fakeGitLab) postedTo(project int64, key string) []string {
	var posted []string
	for _, e := range f.eventsBySession[f.sessionIn(project, key)] {
		posted = append(posted, e.EventName+" "+e.Details["tool_call_id"].(string)) //nolint:forcetypeassert // tool events carry it
	}
	return posted
}

// sessionKeys returns the idempotency and parent keys of each session request.
func (f *fakeGitLab) sessionKeys() [][2]string {
	var keys [][2]string
	for _, r := range f.sessionRequests {
		keys = append(keys, [2]string{r.IdempotencyKey, r.ParentIdempotencyKey})
	}
	return keys
}

// claudeSyncPaths sync Claude Code session sess-1 from the current folder,
// either the way its Stop hook does or the way the fallback sync does.
var claudeSyncPaths = []struct {
	name string
	sync func(t *testing.T, env *syncAllEnv)
}{
	{
		name: "hook",
		sync: func(t *testing.T, env *syncAllEnv) {
			t.Helper()
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
			_, err := env.exec("-R " + testHost + "/my-group/my-project --silent")
			require.NoError(t, err)
		},
	},
	{
		name: "fallback sync",
		sync: func(t *testing.T, env *syncAllEnv) {
			t.Helper()
			if _, err := readSessionMeta("sess-1"); errors.Is(err, errNoSessionMeta) {
				cwd, err := currentCWD()
				require.NoError(t, err)
				path, err := transcriptPath("sess-1", cwd)
				require.NoError(t, err)
				require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
					AgentType: "claude-code", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: path,
				}))
			}
			_, err := env.exec("--all")
			require.NoError(t, err)
		},
	},
}

func TestSync_ClaudeCodeSessionThatMovesToAnotherProject(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.projects["my-group/other"] = 43
			env.expectRemote("/work/a", "my-group/my-project")
			env.expectRemote("/work/b", "my-group/other")
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/b"})

			path.sync(t, env)

			assert.ElementsMatch(t, [][2]string{{"sess-1-42", "sess-1"}, {"sess-1-43", "sess-1"}}, env.fake.sessionKeys(),
				"one GitLab session for each project, sharing the agent session as parent")
			assert.Equal(t, []string{eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1"}, env.fake.postedTo(42, "sess-1-42"))
			assert.Equal(t, []string{eventToolInvoked + " toolu_2", eventToolResponse + " toolu_2"}, env.fake.postedTo(43, "sess-1-43"))
			assertCursorAtEnd(t, transcript)
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.ElementsMatch(t, []sessionPart{
				{projectRef: projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"}, Key: "sess-1-42", ProjectID: 42, SessionID: env.fake.sessionIn(42, "sess-1-42")},
				{projectRef: projectRef{Host: testHost, PathWithNamespace: "my-group/other"}, Key: "sess-1-43", ProjectID: 43, SessionID: env.fake.sessionIn(43, "sess-1-43")},
			}, meta.Parts)
		})
	}
}

func TestSync_FolderWithoutProjectIsNotUploaded(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.projects["my-group/other"] = 43
			env.expectRemote("/work/a", "my-group/my-project")
			env.expectNoRepository("/home/me")
			env.expectRemote("/work/b", "my-group/other")
			transcript := writeClaudeTranscriptIn(t,
				claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_home", "/home/me"}, claudeCall{"toolu_2", "/work/b"})

			path.sync(t, env)

			assert.Equal(t, []string{eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1"}, env.fake.postedTo(42, "sess-1-42"))
			assert.Equal(t, []string{eventToolInvoked + " toolu_2", eventToolResponse + " toolu_2"}, env.fake.postedTo(43, "sess-1-43"))
			assert.Equal(t, 4, env.fake.eventsPosted, "nothing from the home folder is uploaded")
			assertCursorAtEnd(t, transcript)
		})
	}
}

func TestSync_FolderSeenByTheHookKeepsItsProject(t *testing.T) {
	env := newSyncAllEnv(t, true)
	cwd, err := currentCWD()
	require.NoError(t, err)
	transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", cwd})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")

	_, err = env.exec("-R " + testHost + "/my-group/my-project --silent")
	require.NoError(t, err)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	require.NoError(t, os.WriteFile(transcript, claudeTranscriptIn(t, claudeCall{"toolu_1", cwd}, claudeCall{"toolu_2", cwd}), 0o644))
	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{
		eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1",
		eventToolInvoked + " toolu_2", eventToolResponse + " toolu_2",
	}, env.fake.postedTo(42, "sess-1-42"), "the hook's project is used without asking git, which may name another remote")
}

func TestSyncAll_CodexShellCallsInOtherFolders(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	env.fake.projects["my-group/other"] = 43
	env.expectRemote("/work/b", "my-group/other")
	env.expectNoRepository("/tmp/scratch")
	path := writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", "/work/a", "git@"+testHost+":my-group/my-project.git"),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_a", "arguments": `{"cmd":"ls"}`}),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_b", "arguments": `{"cmd":"git push","workdir":"/work/b"}`}),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_tmp", "arguments": `{"cmd":"ls","workdir":"/tmp/scratch"}`}),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call_output", "call_id": "call_b"}),
	))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.ElementsMatch(t, [][2]string{{"th-1-42", "th-1"}, {"th-1-43", "th-1"}}, env.fake.sessionKeys())
	assert.Equal(t, []string{eventToolInvoked + " call_a"}, env.fake.postedTo(42, "th-1-42"))
	assert.Equal(t, []string{eventToolInvoked + " call_b", eventToolResponse + " call_b"}, env.fake.postedTo(43, "th-1-43"),
		"a call's output goes with the call")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assertCursorAt(t, "th-1", info.Size())
}

func TestSync_PartRefusedByItsProject(t *testing.T) {
	tests := []struct {
		name string
		// identity and session answer the other project's endpoints.
		identity, session int
		wantCursor        bool
	}{
		{name: "governance off", session: http.StatusNotFound, wantCursor: true},
		{name: "session refused", session: http.StatusForbidden, wantCursor: true},
		// What a role too low, or a project without the license, answers. As
		// terms not accepted, SAML or an IP allowlist answer it too, it counts
		// as a refusal only because the other project, on the same host, synced.
		{name: "identity refused", identity: http.StatusForbidden, wantCursor: true},
		{name: "not signed in", session: http.StatusUnauthorized},
	}
	for _, path := range claudeSyncPaths {
		for _, tt := range tests {
			t.Run(path.name+"/"+tt.name, func(t *testing.T) {
				env := newSyncAllEnv(t, true)
				env.fake.projects["my-group/other"] = 43
				env.fake.identityStatusFor = map[int64]int{43: tt.identity}
				env.fake.sessionStatusFor = map[int64]int{43: tt.session}
				env.expectRemote("/work/a", "my-group/my-project")
				env.expectRemote("/work/b", "my-group/other")
				transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/b"})

				path.sync(t, env)

				assert.Equal(t, []string{eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1"}, env.fake.postedTo(42, "sess-1-42"),
					"the other project does not hold back this one")
				assert.Zero(t, env.fake.sessionIn(43, "sess-1-43"))
				if tt.wantCursor {
					assertCursorAtEnd(t, transcript)
				} else {
					assertCursorAt(t, "sess-1", 0)
				}
			})
		}
	}
}

func TestSync_SessionFromAnEarlierGlabKeepsItsGitLabSession(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.projects["my-group/other"] = 43
			env.expectRemote("/work/a", "my-group/my-project")
			env.expectRemote("/work/b", "my-group/other")
			synced := claudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"})
			transcript := writeClaudeTranscriptIn(t,
				claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/a"}, claudeCall{"toolu_3", "/work/b"})
			// What an earlier glab recorded, without parts.
			mp, err := metaPath("sess-1")
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(mp), 0o755))
			require.NoError(t, os.WriteFile(mp, []byte(`{"agent_type":"claude-code","path_with_namespace":"my-group/my-project","host":"`+testHost+`","transcript":"`+transcript+`"}`), 0o600))
			require.NoError(t, syncstate.WritePosition("sess-1", int64(len(synced))))

			path.sync(t, env)

			assert.ElementsMatch(t, [][2]string{{"sess-1", "sess-1"}, {"sess-1-43", "sess-1"}}, env.fake.sessionKeys(),
				"the project it was syncing to keeps the GitLab session keyed by the agent session alone")
			assert.Equal(t, []string{eventToolInvoked + " toolu_2", eventToolResponse + " toolu_2"}, env.fake.postedTo(42, "sess-1"))
			assert.Equal(t, []string{eventToolInvoked + " toolu_3", eventToolResponse + " toolu_3"}, env.fake.postedTo(43, "sess-1-43"))
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.Contains(t, meta.Parts, sessionPart{
				projectRef: projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"},
				Key:        "sess-1", ProjectID: 42, SessionID: env.fake.sessionIn(42, "sess-1"),
			})
		})
	}
}

func TestSyncAll_CodexReadInTheMiddleOfATurn(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	env.expectNoRepository("/home/me")
	turn := func(dir string) map[string]any {
		return map[string]any{"timestamp": recentTimestamp(), "type": "turn_context", "payload": map[string]any{"model": "m", "cwd": dir}}
	}
	synced := jsonLines(t,
		codexSessionMetaLine("th-1", "/work/a", "git@"+testHost+":my-group/my-project.git"),
		turn("/work/a"),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_a", "arguments": `{"cmd":"ls"}`}),
		turn("/home/me"),
	)
	path := writeCodexRollout(t, env.home, "th-1", synced+jsonLines(t,
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_home", "arguments": `{"cmd":"cat .ssh/config"}`}),
	))
	project := projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"}
	require.NoError(t, writeSessionMeta("th-1", sessionMeta{
		AgentType: "codex", PathWithNamespace: project.PathWithNamespace, Host: testHost, Transcript: path,
		Folders: map[string]projectRef{"/work/a": project},
	}))
	require.NoError(t, syncstate.WritePosition("th-1", int64(len(synced))))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.sessionRequests, "the turn in progress is in the home folder, not the session's project")
}

func TestSyncAll_CodexFolderNotFoundBeforeTheCursor(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	// A session_meta without a folder stands in for a turn_context too far
	// back for glab to read.
	synced := jsonLines(t, codexSessionMetaLine("th-1", "", "git@"+testHost+":my-group/my-project.git"))
	path := writeCodexRollout(t, env.home, "th-1", synced+jsonLines(t,
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_unknown", "arguments": `{"cmd":"cat .ssh/config"}`}),
		map[string]any{"timestamp": recentTimestamp(), "type": "turn_context", "payload": map[string]any{"model": "m", "cwd": "/work/a"}},
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_a", "arguments": `{"cmd":"ls"}`}),
	))
	project := projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"}
	require.NoError(t, writeSessionMeta("th-1", sessionMeta{
		AgentType: "codex", PathWithNamespace: project.PathWithNamespace, Host: testHost, Transcript: path,
		Folders: map[string]projectRef{"/work/a": project},
	}))
	require.NoError(t, syncstate.WritePosition("th-1", int64(len(synced))))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{eventToolInvoked + " call_a"}, env.fake.postedTo(42, "th-1-42"),
		"a call whose folder glab could not find is not uploaded, unlike one after the next turn_context")
	assert.Equal(t, []string{"codex activity in folders without a GitLab project in the session's top-level groups"}, readStatus(t).Skipped)
}

func TestSync_SubfolderOfAFolderTheHookRanIn(t *testing.T) {
	env := newSyncAllEnv(t, true)
	cwd, err := currentCWD()
	require.NoError(t, err)
	env.expectTopLevel(cwd, cwd)
	env.expectTopLevel(filepath.Join(cwd, "internal"), cwd)
	writeClaudeTranscriptIn(t, claudeCall{"toolu_root", cwd}, claudeCall{"toolu_sub", filepath.Join(cwd, "internal")})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")

	_, err = env.exec("-R " + testHost + "/my-group/my-project --silent")
	require.NoError(t, err)

	assert.Equal(t, []string{
		eventToolInvoked + " toolu_root", eventToolInvoked + " toolu_sub",
		eventToolResponse + " toolu_root", eventToolResponse + " toolu_sub",
	}, env.fake.postedTo(42, "sess-1-42"), "a subfolder goes where the hook sent its repository, without asking git, which would name the origin remote")
}

func TestSync_RepositoryClonedInsideAFolderTheHookRanIn(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projects["my-group/other"] = 43
	cwd, err := currentCWD()
	require.NoError(t, err)
	clone := filepath.Join(cwd, "other")
	env.expectTopLevel(clone, clone)
	env.expectTopLevel(cwd, cwd)
	env.expectRemote(clone, "my-group/other")
	writeClaudeTranscriptIn(t, claudeCall{"toolu_root", cwd}, claudeCall{"toolu_clone", clone})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")

	_, err = env.exec("-R " + testHost + "/my-group/my-project --silent")
	require.NoError(t, err)

	assert.Equal(t, []string{eventToolInvoked + " toolu_root", eventToolResponse + " toolu_root"}, env.fake.postedTo(42, "sess-1-42"))
	assert.Equal(t, []string{eventToolInvoked + " toolu_clone", eventToolResponse + " toolu_clone"}, env.fake.postedTo(43, "sess-1-43"),
		"a repository inside the one the hook ran in goes to its own project")
}

func TestPushSession_KeepsCallsAnotherRunRecorded(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.expectNoRepository("/home/me")
	project := projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"}
	require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
		AgentType: "claude-code", PathWithNamespace: project.PathWithNamespace, Host: testHost,
		PendingCalls: map[string]projectRef{"toolu_other": project, "toolu_done": {}},
	}))
	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := config.NewFromString("hosts:\n  " + testHost + ":\n    token: test-token\n")
	s := newSyncer(&options{io: ios, silent: true, executor: env.mExec, config: func() config.Config { return cfg }})
	data := &sessionData{
		ToolCalls:   []toolCall{{ID: "toolu_home", CWD: "/home/me"}},
		ToolResults: []toolResult{{CallID: "toolu_done", CWD: "/home/me"}},
	}

	// The record this run read was taken before the other run recorded its calls.
	s.pushSession(t.Context(), "sess-1", "claude-code", claudeTranscript{}, data, 10, false, project, &sessionMeta{})

	meta, err := readSessionMeta("sess-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]projectRef{"toolu_other": project, "toolu_home": {}}, meta.PendingCalls)
}

// writeEarlierGlabRecord writes what a glab from before sessions were split
// by project recorded for session sess-1, whose transcript is at transcript.
func writeEarlierGlabRecord(t *testing.T, transcript string) {
	t.Helper()
	mp, err := metaPath("sess-1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(mp), 0o755))
	record := `{"agent_type":"claude-code","path_with_namespace":"my-group/my-project","host":"` + testHost + `","transcript":"` + transcript + `"}`
	require.NoError(t, os.WriteFile(mp, []byte(record), 0o600))
}

func TestSync_SessionFromAnEarlierGlabWithNothingUploaded(t *testing.T) {
	tests := []struct {
		name string
		// end ends the session the way the path does.
		end           func(t *testing.T, env *syncAllEnv, transcript string)
		wantCompleted bool
	}{
		{
			name: "SessionEnd hook",
			end: func(t *testing.T, env *syncAllEnv, _ string) {
				t.Helper()
				t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
				_, err := env.exec("-R " + testHost + "/my-group/my-project --silent --complete")
				require.NoError(t, err)
			},
		},
		{
			name: "idle in the fallback sync",
			end: func(t *testing.T, env *syncAllEnv, transcript string) {
				t.Helper()
				idle := time.Now().Add(-idleCompleteAfter - time.Hour)
				require.NoError(t, os.Chtimes(transcript, idle, idle))
				_, err := env.exec("--all")
				require.NoError(t, err)
			},
		},
		{
			name: "too old for GitLab in the fallback sync",
			end: func(t *testing.T, env *syncAllEnv, transcript string) {
				t.Helper()
				old := time.Now().Add(-100 * 24 * time.Hour)
				require.NoError(t, os.Chtimes(transcript, old, old))
				_, err := env.exec("--all")
				require.NoError(t, err)
			},
			wantCompleted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			transcript := writeClaudeTranscriptIn(t)
			writeEarlierGlabRecord(t, transcript)

			tt.end(t, env, transcript)

			assert.Empty(t, env.fake.sessionRequests, "the GitLab session the earlier glab would use was never created, so it is not created empty")
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.Equal(t, tt.wantCompleted, meta.CompletedAt != nil)
		})
	}
}

func TestSyncAll_FolderWhoseProjectDoesNotExist(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.expectRemote("/work/a", "my-group/my-project")
	env.expectRemote("/work/x", "my-group/not-created-yet")
	transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_x", "/work/x"})
	require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
		AgentType: "claude-code", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: transcript,
	}))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1"}, env.fake.postedTo(42, "sess-1-42"))
	assertCursorAtEnd(t, transcript)
	status := readStatus(t)
	assert.Equal(t, []string{testHost + "/my-group/not-created-yet"}, status.ProjectNotFound)
	assert.Empty(t, status.Errors)

	env.fake.projects["my-group/not-created-yet"] = 43
	_, err = env.exec("--all")
	require.NoError(t, err)
	assert.Zero(t, env.fake.sessionIn(43, "sess-1-43"), "activity from before the project existed is not uploaded once it does")
}

func TestSyncAll_ResultOfACallReadInAnEarlierSync(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projects["my-group/other"] = 43
	env.expectRemote("/work/b", "my-group/other")
	call, err := json.Marshal(map[string]any{
		"type": "assistant", "sessionId": "sess-1", "cwd": "/work/b", "timestamp": time.Now().Format(time.RFC3339),
		"message": map[string]any{"content": []map[string]any{{"type": "tool_use", "id": "toolu_cd", "name": "Bash"}}},
	})
	require.NoError(t, err)
	result, err := json.Marshal(map[string]any{
		"type": "user", "sessionId": "sess-1", "cwd": "/work/a", "timestamp": time.Now().Format(time.RFC3339),
		"message": map[string]any{"content": []map[string]any{{"type": "tool_result", "tool_use_id": "toolu_cd"}}},
	})
	require.NoError(t, err)
	transcript := writeClaudeTranscriptIn(t)
	require.NoError(t, os.WriteFile(transcript, append(call, '\n'), 0o644))
	require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
		AgentType: "claude-code", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: transcript,
	}))
	_, err = env.exec("--all")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(transcript, append(append(call, '\n'), append(result, '\n')...), 0o644))
	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{eventToolInvoked + " toolu_cd", eventToolResponse + " toolu_cd"}, env.fake.postedTo(43, "sess-1-43"),
		"the result goes with its call, though it was recorded after a cd")
	assert.Zero(t, env.fake.sessionIn(42, "sess-1-42"))
	meta, err := readSessionMeta("sess-1")
	require.NoError(t, err)
	assert.Empty(t, meta.PendingCalls)
}

func TestSyncAll_ProjectOnAnInstanceWithoutTheAgentType(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projects["my-group/other"] = 43
	// What GitLab versions without the agent type answer.
	env.fake.identityStatusFor = map[int64]int{43: http.StatusBadRequest}
	env.fake.identityBody = `{"error":"agent_type does not have a valid value"}`
	env.expectRemote("/work/a", "my-group/my-project")
	env.expectRemote("/work/b", "my-group/other")
	transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/b"})
	require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
		AgentType: "claude-code", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: transcript,
	}))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1"}, env.fake.postedTo(42, "sess-1-42"))
	assertCursorAtEnd(t, transcript)
	assert.Equal(t, []string{"claude-code on " + testHost}, readStatus(t).AgentTypeNotSupported)
	meta, err := readSessionMeta("sess-1")
	require.NoError(t, err)
	assert.Nil(t, meta.CompletedAt, "the session goes on in the projects that accept it")
	requests := env.fake.identityRequests

	env.expectRemote("/work/b", "my-group/other")
	appendToFile(t, transcript, claudeTranscriptIn(t, claudeCall{"toolu_3", "/work/b"}))
	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, requests, env.fake.identityRequests, "the project is not tried again, so nothing is uploaded there after an upgrade")
	assert.Zero(t, env.fake.sessionIn(43, "sess-1-43"))
}

func TestSync_FolderOutsideTheSessionsTopLevelGroup(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		// project is the folder's project, and id its ID, when it exists.
		project    string
		id         int64
		wantRouted bool
	}{
		{name: "another top-level group", remote: "git@" + testHost + ":other-org/project.git", project: "other-org/project", id: 50},
		{name: "another host", remote: "git@other.example.com:my-group/project.git"},
		{name: "another subgroup of the same top-level group", remote: "git@" + testHost + ":my-group/sub/project.git", project: "my-group/sub/project", id: 51, wantRouted: true},
	}
	for _, path := range claudeSyncPaths {
		for _, tt := range tests {
			t.Run(path.name+"/"+tt.name, func(t *testing.T) {
				env := newSyncAllEnvWithConfig(t, true, "hosts:\n  "+testHost+":\n    token: test-token\n  other.example.com:\n    token: test-token\n")
				if tt.project != "" {
					env.fake.projects[tt.project] = tt.id
				}
				env.expectRemote("/work/a", "my-group/my-project")
				env.mExec.EXPECT().
					ExecWithIO(gomock.Any(), "git", []string{"-C", "/work/x", "config", "--get", "remote.origin.url"}, nil, nil, gomock.Any(), gomock.Any()).
					DoAndReturn(writesStdout(tt.remote + "\n"))
				transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_x", "/work/x"})

				path.sync(t, env)

				assert.Equal(t, []string{eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1"}, env.fake.postedTo(42, "sess-1-42"))
				assertCursorAtEnd(t, transcript)
				if tt.wantRouted {
					assert.Equal(t, []string{eventToolInvoked + " toolu_x", eventToolResponse + " toolu_x"}, env.fake.postedTo(tt.id, fmt.Sprintf("sess-1-%d", tt.id)))
					return
				}
				assert.Len(t, env.fake.sessionRequests, 1, "nothing is uploaded to a project outside the session's top-level group")
				assert.Equal(t, 2, env.fake.eventsPosted, "and nothing from that folder goes to the session's project")
				if path.name == "fallback sync" {
					assert.Equal(t, []string{"claude-code activity in folders without a GitLab project in the session's top-level groups"}, readStatus(t).Skipped)
				}
			})
		}
	}
}

func TestSync_HookInAnotherTopLevelGroupDoesNotMoveTheAnchor(t *testing.T) {
	for _, path := range []string{"hook", "fallback sync"} {
		t.Run(path, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.projects["other-org/project"] = 50
			cwd, err := currentCWD()
			require.NoError(t, err)
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", cwd})
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
			_, err = env.exec("-R " + testHost + "/my-group/my-project --silent")
			require.NoError(t, err)

			// The agent changed to a clone of another organization's project,
			// and its Stop hook ran there.
			if path == "hook" {
				appendToFile(t, transcript, claudeTranscriptIn(t, claudeCall{"toolu_2", cwd}))
				_, err = env.exec("-R " + testHost + "/other-org/project --silent")
				require.NoError(t, err)
			} else {
				_, err = env.exec("-R " + testHost + "/other-org/project --silent")
				require.NoError(t, err)
				appendToFile(t, transcript, claudeTranscriptIn(t, claudeCall{"toolu_2", cwd}))
				_, err = env.exec("--all")
				require.NoError(t, err)
				assert.Equal(t, []string{"claude-code activity in folders without a GitLab project in the session's top-level groups"}, readStatus(t).Skipped)
			}

			assert.Equal(t, []string{eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1"}, env.fake.postedTo(42, "sess-1-42"))
			assert.Zero(t, env.fake.sessionIn(50, "sess-1-50"), "the other organization's project gets nothing")
			assertCursorAtEnd(t, transcript)
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.Equal(t, "other-org/project", meta.PathWithNamespace)
			assert.Equal(t, projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"}, meta.FirstProject, "the first project does not change")
		})
	}
}

func TestSync_CompletesEveryProjectTheSessionWorkedIn(t *testing.T) {
	tests := []struct {
		name string
		// complete ends the session the way the path does, after its earlier
		// syncs.
		complete func(t *testing.T, env *syncAllEnv, transcript string)
	}{
		{
			name: "SessionEnd hook",
			complete: func(t *testing.T, env *syncAllEnv, _ string) {
				t.Helper()
				t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
				_, err := env.exec("-R " + testHost + "/my-group/my-project --silent --complete")
				require.NoError(t, err)
			},
		},
		{
			name: "idle in the fallback sync",
			complete: func(t *testing.T, env *syncAllEnv, transcript string) {
				t.Helper()
				t.Setenv("CLAUDE_CODE_SESSION_ID", "")
				idle := time.Now().Add(-idleCompleteAfter - time.Hour)
				require.NoError(t, os.Chtimes(transcript, idle, idle))
				_, err := env.exec("--all")
				require.NoError(t, err)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.projects["my-group/other"] = 43
			env.expectRemote("/work/a", "my-group/my-project")
			env.expectRemote("/work/b", "my-group/other")
			env.expectRemote("/work/b", "my-group/other")
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"})
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
			_, err := env.exec("-R " + testHost + "/my-group/my-project --silent")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(transcript, claudeTranscriptIn(t,
				claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/b"}), 0o644))
			_, err = env.exec("-R " + testHost + "/my-group/my-project --silent")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(transcript, claudeTranscriptIn(t,
				claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/b"}, claudeCall{"toolu_3", "/work/b"}), 0o644))

			tt.complete(t, env, transcript)

			assert.ElementsMatch(t, []int{env.fake.sessionIn(42, "sess-1-42"), env.fake.sessionIn(43, "sess-1-43")}, env.fake.completedSessions,
				"the project the last turn did not touch is completed too")
			assert.Equal(t, []string{eventToolInvoked + " toolu_2", eventToolResponse + " toolu_2", eventToolInvoked + " toolu_3", eventToolResponse + " toolu_3"},
				env.fake.postedTo(43, "sess-1-43"))
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.NotNil(t, meta.CompletedAt)
		})
	}
}

func TestSyncAll_SessionStaysOpenUntilEveryProjectIsCompleted(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projects["my-group/other"] = 43
	env.fake.completeStatusFor = map[int64]int{43: http.StatusUnauthorized}
	env.expectRemote("/work/a", "my-group/my-project")
	env.expectRemote("/work/b", "my-group/other")
	transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/b"})
	require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
		AgentType: "claude-code", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: transcript,
	}))
	idle := time.Now().Add(-idleCompleteAfter - time.Hour)
	require.NoError(t, os.Chtimes(transcript, idle, idle))

	_, err := env.exec("--all")
	require.NoError(t, err)

	meta, err := readSessionMeta("sess-1")
	require.NoError(t, err)
	assert.Nil(t, meta.CompletedAt, "a project that could not be completed is retried")
	assert.Zero(t, readStatus(t).SessionsCompleted)
	assertCursorAtEnd(t, transcript)

	env.fake.completeStatusFor = nil
	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Contains(t, env.fake.completedSessions, env.fake.sessionIn(43, "sess-1-43"))
	assert.Equal(t, 1, readStatus(t).SessionsCompleted)
	assert.Equal(t, 4, env.fake.eventsPosted, "events are not posted again")
}

func TestSyncAll_ReportsWhyAProjectWasNotSynced(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projects["my-group/other"] = 43
	env.fake.projects["my-group/off"] = 44
	env.fake.sessionStatusFor = map[int64]int{43: http.StatusForbidden, 44: http.StatusNotFound}
	env.expectRemote("/work/a", "my-group/my-project")
	env.expectRemote("/work/b", "my-group/other")
	env.expectRemote("/work/c", "my-group/off")
	env.expectNoRepository("/home/me")
	transcript := writeClaudeTranscriptIn(t,
		claudeCall{"toolu_1", "/work/a"}, claudeCall{"toolu_2", "/work/b"}, claudeCall{"toolu_3", "/work/c"}, claudeCall{"toolu_4", "/home/me"})
	require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
		AgentType: "claude-code", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: transcript,
	}))

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	assert.Equal(t, 1, status.SessionsSynced)
	assert.Equal(t, 2, status.EventsPosted)
	require.Len(t, status.Errors, 1)
	assert.Contains(t, status.Errors[0], "session sess-1: project "+testHost+"/my-group/other refused the session")
	assert.Equal(t, []string{testHost + "/my-group/off"}, status.GovernanceNotEnabled)
	assert.Equal(t, []string{"claude-code activity in folders without a GitLab project in the session's top-level groups"}, status.Skipped)
	assertCursorAtEnd(t, transcript)
}

func TestRunSync_SaysWhyAProjectWasNotSynced(t *testing.T) {
	env := newSyncAllEnv(t, true)
	env.fake.projects["my-group/other"] = 43
	env.fake.projects["my-group/off"] = 44
	env.fake.sessionStatusFor = map[int64]int{43: http.StatusForbidden, 44: http.StatusNotFound}
	env.expectRemote("/work/b", "my-group/other")
	env.expectRemote("/work/c", "my-group/off")
	env.expectNoRepository("/home/me")
	writeClaudeTranscriptIn(t, claudeCall{"toolu_2", "/work/b"}, claudeCall{"toolu_3", "/work/c"}, claudeCall{"toolu_4", "/home/me"})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")

	out, err := env.exec("-R " + testHost + "/my-group/my-project")
	require.NoError(t, err)

	assert.Contains(t, out.String(), "Not syncing the activity in folders without a GitLab project in the session's top-level groups.")
	assert.Contains(t, out.String(), testHost+"/my-group/off: "+governanceNotEnabled)
	assert.Contains(t, out.Stderr(), "warning: project "+testHost+"/my-group/other refused the session")
}

func TestSync_SessionEndWithoutActivityCreatesNoSession(t *testing.T) {
	tests := []struct {
		name string
		end  func(t *testing.T, env *syncAllEnv, transcript string)
	}{
		{
			name: "SessionEnd hook",
			end: func(t *testing.T, env *syncAllEnv, _ string) {
				t.Helper()
				t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
				_, err := env.exec("-R " + testHost + "/my-group/my-project --silent --complete")
				require.NoError(t, err)
			},
		},
		{
			name: "idle in the fallback sync",
			end: func(t *testing.T, env *syncAllEnv, transcript string) {
				t.Helper()
				require.NoError(t, writeSessionMeta("sess-1", sessionMeta{
					AgentType: "claude-code", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: transcript,
				}))
				idle := time.Now().Add(-idleCompleteAfter - time.Hour)
				require.NoError(t, os.Chtimes(transcript, idle, idle))
				_, err := env.exec("--all")
				require.NoError(t, err)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			transcript := writeClaudeTranscriptIn(t)

			tt.end(t, env, transcript)

			assert.Empty(t, env.fake.sessionRequests, "a session with no activity is not created on GitLab")
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.Nil(t, meta.CompletedAt, "a session with nothing uploaded is kept, in case the user resumes it")
		})
	}
}

func TestSync_PostsToAKnownGitLabSessionWithoutLookingItUp(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.projects["my-group/other"] = 43
			env.expectRemote("/work/b", "my-group/other")
			env.expectRemote("/work/b", "my-group/other")
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", "/work/b"})
			path.sync(t, env)
			lookups, identities, sessions := env.fake.projectLookups["my-group/other"], env.fake.identityRequests, len(env.fake.sessionRequests)

			appendToFile(t, transcript, claudeTranscriptIn(t, claudeCall{"toolu_2", "/work/b"}))
			path.sync(t, env)

			assert.Equal(t, lookups, env.fake.projectLookups["my-group/other"])
			assert.Equal(t, identities, env.fake.identityRequests)
			assert.Len(t, env.fake.sessionRequests, sessions, "GitLab limits how often sessions are created")
			assert.Equal(t, []string{
				eventToolInvoked + " toolu_1", eventToolResponse + " toolu_1",
				eventToolInvoked + " toolu_2", eventToolResponse + " toolu_2",
			}, env.fake.postedTo(43, "sess-1-43"))
		})
	}
}

func TestSync_GitLabSessionThatIsGoneIsCreatedAgain(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", ""})
			path.sync(t, env)
			gone := env.fake.sessionIn(42, "sess-1-42")
			require.NotZero(t, gone)
			env.fake.deleteSession(gone)

			appendToFile(t, transcript, claudeTranscriptIn(t, claudeCall{"toolu_2", ""}))
			path.sync(t, env)

			again := env.fake.sessionIn(42, "sess-1-42")
			require.NotZero(t, again, "the session is created again under its key")
			assert.Equal(t, []string{eventToolInvoked + " toolu_2", eventToolResponse + " toolu_2"}, env.fake.postedTo(42, "sess-1-42"))
			assertCursorAtEnd(t, transcript)
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			part, ok := meta.part(meta.project())
			require.True(t, ok)
			assert.Equal(t, again, part.SessionID)
			assert.Empty(t, readStatusIfAny(t).GovernanceNotEnabled, "a session that is gone is not governance being off")
		})
	}
}

func TestSync_GitLabSessionThatIsGoneIsCreatedAgainToComplete(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", ""})
			path.sync(t, env)
			gone := env.fake.sessionIn(42, "sess-1-42")
			require.NotZero(t, gone)
			env.fake.deleteSession(gone)

			completeWithoutNewActivity(t, env, path.name, transcript)

			again := env.fake.sessionIn(42, "sess-1-42")
			require.NotZero(t, again, "the session is created again under its key")
			assert.NotEqual(t, gone, again)
			assert.Equal(t, []int{again}, env.fake.completedSessions, "the new session is completed")
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			part, ok := meta.part(meta.project())
			require.True(t, ok)
			assert.Equal(t, again, part.SessionID)
			assert.NotNil(t, meta.CompletedAt)
		})
	}
}

func TestSync_SessionIsNotCompletedWhileGovernanceIsOff(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", ""})
			path.sync(t, env)
			env.fake.deleteSession(env.fake.sessionIn(42, "sess-1-42"))
			env.fake.sessionStatus = http.StatusNotFound
			env.fake.identityStatus = http.StatusNotFound

			completeWithoutNewActivity(t, env, path.name, transcript)

			assert.Empty(t, env.fake.completedSessions)
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.Nil(t, meta.CompletedAt, "the session waits until governance is back on")
			if path.name != "hook" {
				assert.NotEmpty(t, readStatusIfAny(t).GovernanceNotEnabled)
			}

			env.fake.sessionStatus = 0
			env.fake.identityStatus = 0
			completeWithoutNewActivity(t, env, path.name, transcript)

			again := env.fake.sessionIn(42, "sess-1-42")
			require.NotZero(t, again)
			assert.Equal(t, []int{again}, env.fake.completedSessions)
			meta, err = readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.NotNil(t, meta.CompletedAt)
		})
	}
}

func TestSync_SessionInAProjectThatIsGoneIsCompleted(t *testing.T) {
	for _, path := range claudeSyncPaths {
		t.Run(path.name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			env.fake.projects["my-group/other"] = 43
			env.expectRemote("/work/b", "my-group/other")
			transcript := writeClaudeTranscriptIn(t, claudeCall{"toolu_1", ""}, claudeCall{"toolu_2", "/work/b"})
			path.sync(t, env)
			env.fake.deleteSession(env.fake.sessionIn(43, "sess-1-43"))
			delete(env.fake.projects, "my-group/other")

			completeWithoutNewActivity(t, env, path.name, transcript)

			assert.Equal(t, []int{env.fake.sessionIn(42, "sess-1-42")}, env.fake.completedSessions)
			meta, err := readSessionMeta("sess-1")
			require.NoError(t, err)
			assert.NotNil(t, meta.CompletedAt, "a project that is gone does not keep the session open")
		})
	}
}

// completeWithoutNewActivity ends the session the way the hook or the
// fallback sync named by path does, with nothing new in transcript.
func completeWithoutNewActivity(t *testing.T, env *syncAllEnv, path, transcript string) {
	t.Helper()
	if path == "hook" {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
		_, err := env.exec("-R " + testHost + "/my-group/my-project --silent --complete")
		require.NoError(t, err)
		return
	}
	idle := time.Now().Add(-idleCompleteAfter - time.Hour)
	require.NoError(t, os.Chtimes(transcript, idle, idle))
	_, err := env.exec("--all")
	require.NoError(t, err)
}

// readStatusIfAny returns the status of the last --all run, or an empty one
// when only hooks ran.
func readStatusIfAny(t *testing.T) *fallbacksync.Status {
	t.Helper()
	status, err := fallbacksync.ReadStatus()
	if errors.Is(err, fallbacksync.ErrNoStatus) {
		return &fallbacksync.Status{}
	}
	require.NoError(t, err)
	return status
}
