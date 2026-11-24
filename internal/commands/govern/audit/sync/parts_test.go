//go:build !integration

package sync

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestSplitByProject(t *testing.T) {
	t.Parallel()

	projectA := projectRef{Host: testHost, PathWithNamespace: "group/a"}
	projectB := projectRef{Host: testHost, PathWithNamespace: "group/b"}
	folders := map[string]projectRef{
		"/work/a":     projectA,
		"/work/a/sub": projectA,
		"/work/A":     {Host: testHost, PathWithNamespace: "Group/A"},
		"/work/b":     projectB,
	}
	projectOf := func(dir string) projectRef {
		if dir == "" {
			return projectA
		}
		return folders[dir]
	}
	at := func(s int) time.Time { return time.Date(2026, 10, 9, 1, 0, s, 0, time.UTC) }

	type want struct {
		project   projectRef
		prompts   []string
		calls     []string
		results   []string
		responses []string
		goal      string
		startedAt time.Time
	}
	tests := []struct {
		name        string
		data        *sessionData
		pending     map[string]projectRef
		want        []want
		wantDropped int
		wantPending map[string]projectRef
	}{
		{
			name: "a Claude Code session that moves from one project to another",
			data: &sessionData{
				Goal: "clone b", StartedAt: at(0),
				Prompts: []prompt{
					{ID: "u1", Text: "clone b", Timestamp: at(0), CWD: "/work/a"},
					{ID: "u2", Text: "now in b", Timestamp: at(3), CWD: "/work/b"},
				},
				ToolCalls:   []toolCall{{ID: "toolu_1", Timestamp: at(1), CWD: "/work/a"}, {ID: "toolu_2", Timestamp: at(4), CWD: "/work/b"}},
				ToolResults: []toolResult{{CallID: "toolu_1", Timestamp: at(2), CWD: "/work/a"}},
				Responses:   []llmResponse{{ID: "msg_1", Timestamp: at(1), CWD: "/work/a"}, {ID: "msg_2", Timestamp: at(4), CWD: "/work/b"}},
			},
			want: []want{
				{project: projectA, prompts: []string{"u1"}, calls: []string{"toolu_1"}, results: []string{"toolu_1"}, responses: []string{"msg_1"}, goal: "clone b", startedAt: at(0)},
				{project: projectB, prompts: []string{"u2"}, calls: []string{"toolu_2"}, responses: []string{"msg_2"}, goal: "now in b", startedAt: at(3)},
			},
			wantPending: map[string]projectRef{"toolu_2": projectB},
		},
		{
			name: "Codex shell calls that each run in their own folder",
			data: &sessionData{
				Prompts:     []prompt{{ID: "p1", Text: "update both", Timestamp: at(0), CWD: "/work/a"}},
				ToolCalls:   []toolCall{{ID: "call_b", Timestamp: at(1), CWD: "/work/b"}, {ID: "call_sub", Timestamp: at(2), CWD: "/work/a/sub"}},
				ToolResults: []toolResult{{CallID: "call_b", Timestamp: at(3), CWD: "/work/b"}},
			},
			want: []want{
				{project: projectA, prompts: []string{"p1"}, calls: []string{"call_sub"}, goal: "update both", startedAt: at(0)},
				{project: projectB, calls: []string{"call_b"}, results: []string{"call_b"}, startedAt: at(1)},
			},
			wantPending: map[string]projectRef{"call_sub": projectA},
		},
		{
			name: "a folder without a GitLab project in between",
			data: &sessionData{
				Prompts: []prompt{
					{ID: "u1", Text: "work in a", Timestamp: at(0), CWD: "/work/a"},
					{ID: "u2", Text: "look around home", Timestamp: at(1), CWD: "/home/me"},
					{ID: "u3", Text: "back in b", Timestamp: at(3), CWD: "/work/b"},
				},
				ToolCalls:   []toolCall{{ID: "toolu_home", Timestamp: at(2), CWD: "/home/me"}},
				ToolResults: []toolResult{{CallID: "toolu_home", Timestamp: at(2), CWD: "/home/me"}},
			},
			want: []want{
				{project: projectA, prompts: []string{"u1"}, goal: "work in a", startedAt: at(0)},
				{project: projectB, prompts: []string{"u3"}, goal: "back in b", startedAt: at(3)},
			},
			wantDropped: 3,
		},
		{
			name: "entries without a folder go to the session's project",
			data: &sessionData{
				Goal: "from the export", StartedAt: at(0),
				ToolCalls: []toolCall{{ID: "call_1", Timestamp: at(5)}},
			},
			want: []want{
				{project: projectA, calls: []string{"call_1"}, goal: "from the export", startedAt: at(0)},
			},
		},
		{
			name: "a project spelled with other case is one part",
			data: &sessionData{
				ToolCalls: []toolCall{{ID: "toolu_1", Timestamp: at(0), CWD: "/work/a"}, {ID: "toolu_2", Timestamp: at(1), CWD: "/work/A"}},
			},
			want: []want{
				{project: projectA, calls: []string{"toolu_1", "toolu_2"}, startedAt: at(0)},
			},
			wantPending: map[string]projectRef{"toolu_1": projectA, "toolu_2": folders["/work/A"]},
		},
		{
			name: "results of calls read in an earlier sync",
			data: &sessionData{
				ToolCalls: []toolCall{{ID: "toolu_new", Timestamp: at(3), CWD: "/work/a"}},
				ToolResults: []toolResult{
					{CallID: "toolu_b", Timestamp: at(1), CWD: "/work/a"},
					{CallID: "toolu_home", Timestamp: at(2), CWD: "/work/a"},
				},
			},
			pending: map[string]projectRef{"toolu_b": projectB, "toolu_home": {}, "toolu_open": projectA},
			want: []want{
				{project: projectA, calls: []string{"toolu_new"}, startedAt: at(3)},
				{project: projectB, results: []string{"toolu_b"}, startedAt: at(1)},
			},
			wantDropped: 1,
			wantPending: map[string]projectRef{"toolu_open": projectA, "toolu_new": projectA},
		},
		{
			name: "only folders without a project",
			data: &sessionData{
				Prompts: []prompt{{ID: "u1", Text: "hi", Timestamp: at(0), CWD: "/home/me"}},
			},
			wantDropped: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			split := splitByProject(tt.data, projectOf, tt.pending)

			assert.Equal(t, tt.wantDropped, split.dropped)
			if tt.wantPending == nil {
				tt.wantPending = map[string]projectRef{}
			}
			assert.Equal(t, tt.wantPending, split.pending)
			require.Len(t, split.parts, len(tt.want))
			for i, w := range tt.want {
				got := split.parts[i].data
				assert.Equal(t, w.project.key(), split.parts[i].project.key())
				assert.Equal(t, w.prompts, collect(got.Prompts, func(p prompt) string { return p.ID }), "prompts of %s", w.project)
				assert.Equal(t, w.calls, collect(got.ToolCalls, func(c toolCall) string { return c.ID }), "calls of %s", w.project)
				assert.Equal(t, w.results, collect(got.ToolResults, func(r toolResult) string { return r.CallID }), "results of %s", w.project)
				assert.Equal(t, w.responses, collect(got.Responses, func(r llmResponse) string { return r.ID }), "responses of %s", w.project)
				assert.Equal(t, w.goal, got.Goal, "goal of %s", w.project)
				assert.Equal(t, w.startedAt, got.StartedAt, "start of %s", w.project)
			}
		})
	}
}

func collect[T any](items []T, id func(T) string) []string {
	var ids []string
	for _, item := range items {
		ids = append(ids, id(item))
	}
	return ids
}

func TestSessionRouter(t *testing.T) {
	t.Parallel()

	recorded := projectRef{Host: testHost, PathWithNamespace: "group/recorded"}
	origin := projectRef{Host: testHost, PathWithNamespace: "group/origin"}
	session := projectRef{Host: testHost, PathWithNamespace: "group/session"}
	nested := projectRef{Host: testHost, PathWithNamespace: "group/lib"}
	topLevels := map[string]string{
		"/work/a":                "/work/a",
		"/work/a/internal/x":     "/work/a",
		"/work/a/vendor/lib":     "/work/a/vendor/lib",
		"/work/a/vendor/lib/src": "/work/a/vendor/lib",
		"/work/a/clone":          "/work/a/clone",
	}
	var looked []string
	r := sessionRouter{
		known:   map[string]projectRef{"/work/a": recorded, "/work/a/vendor/lib": nested},
		session: session,
		lookup: func(dir string) projectRef {
			looked = append(looked, dir)
			return origin
		},
		topLevel: func(dir string) string { return topLevels[dir] },
	}

	assert.Equal(t, recorded, r.project("/work/a"), "a folder a hook resolved keeps that project")
	assert.Equal(t, recorded, r.project("/work/a/internal/x"), "and so do its subfolders in the same repository")
	assert.Equal(t, nested, r.project("/work/a/vendor/lib/src"), "the closest recorded folder wins")
	assert.Equal(t, origin, r.project("/work/a/clone"), "a repository cloned inside a recorded one is looked up")
	assert.Equal(t, origin, r.project("/work/a/scratch"), "a folder outside any repository is looked up")
	assert.Equal(t, origin, r.project("/work/b"))
	assert.Equal(t, origin, r.project("/work/ab"), "a folder that only shares a prefix is not a subfolder")
	assert.Equal(t, session, r.project(""))
	assert.Equal(t, []string{"/work/a/clone", "/work/a/scratch", "/work/b", "/work/ab"}, looked)
}

func TestFolderProjects(t *testing.T) {
	t.Parallel()

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	cfg := config.NewFromString("hosts:\n  " + testHost + ":\n    token: test-token\n")
	gitRemote := func(dir string) *cmdtest.MockExecutorExecWithIOCall {
		return mExec.EXPECT().
			ExecWithIO(gomock.Any(), "git", []string{"-C", dir, "config", "--get", "remote.origin.url"}, nil, nil, gomock.Any(), gomock.Any())
	}
	gitRemote("/work/a").DoAndReturn(writesStdout("git@" + testHost + ":group/a.git\n")).Times(1)
	gitRemote("/work/gh").DoAndReturn(writesStdout("git@github.com:someone/repo.git\n")).Times(1)
	gitRemote("/home/me").Return(errors.New("not a git repository")).Times(1)
	f := newFolderProjects(mExec, func() config.Config { return cfg })

	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "git", []string{"-C", "/work/a/sub", "rev-parse", "--show-toplevel"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(writesStdout("/work/a\n")).Times(1)
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "git", []string{"-C", "/home/me", "rev-parse", "--show-toplevel"}, nil, nil, gomock.Any(), gomock.Any()).
		Return(errors.New("not a git repository")).Times(1)

	for range 2 {
		assert.Equal(t, projectRef{Host: testHost, PathWithNamespace: "group/a"}, f.lookup(t.Context(), "/work/a"))
		assert.Zero(t, f.lookup(t.Context(), "/work/gh"), "a host glab is not logged in to")
		assert.Zero(t, f.lookup(t.Context(), "/home/me"))
		assert.Equal(t, "/work/a", f.topLevel(t.Context(), "/work/a/sub"))
		assert.Empty(t, f.topLevel(t.Context(), "/home/me"))
	}
}
