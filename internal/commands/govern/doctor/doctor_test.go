//go:build !integration

package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/opencodeplugin"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCheckClaudeHooks_HooksPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existingHooks := map[string][]claudehooks.HookGroup{
		"Stop": {
			{Hooks: []claudehooks.HookEntry{{Type: "command", Command: claudehooks.StopHookCommand}}},
		},
		"SessionEnd": {
			{Hooks: []claudehooks.HookEntry{{Type: "command", Command: claudehooks.SessionEndHookCommand}}},
		},
	}

	hooksJSON, err := json.Marshal(existingHooks)
	require.NoError(t, err)

	raw := map[string]json.RawMessage{"hooks": hooksJSON}
	require.NoError(t, claudehooks.WriteSettings(path, raw))

	// Override the settings path for testing
	t.Setenv("HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude"), 0o755))
	require.NoError(t, claudehooks.WriteSettings(filepath.Join(dir, ".claude", "settings.json"), raw))

	result := checkClaudeHooks()

	assert.True(t, result.ok)
	assert.Equal(t, "Stop and SessionEnd hooks installed", result.message)
}

func TestCheckClaudeHooks_HooksMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// No settings file exists
	result := checkClaudeHooks()

	assert.True(t, result.ok, "missing Claude Code should be advisory")
	assert.Contains(t, result.message, "No hook-based agent detected")
}

func TestCheckClaudeHooks_StopHookMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Only SessionEnd hook present
	existingHooks := map[string][]claudehooks.HookGroup{
		"SessionEnd": {
			{Hooks: []claudehooks.HookEntry{{Type: "command", Command: claudehooks.SessionEndHookCommand}}},
		},
	}

	hooksJSON, err := json.Marshal(existingHooks)
	require.NoError(t, err)

	raw := map[string]json.RawMessage{"hooks": hooksJSON}
	require.NoError(t, claudehooks.WriteSettings(filepath.Join(dir, ".claude", "settings.json"), raw))

	result := checkClaudeHooks()

	assert.False(t, result.ok)
	assert.Contains(t, result.message, "Stop")
}

func TestCheckGlabInPath_NotFound(t *testing.T) {
	t.Setenv("PATH", "")

	result := checkGlabInPath()

	assert.False(t, result.ok)
	assert.Contains(t, result.message, "not found")
	assert.NotEmpty(t, result.fix)
}

func TestNewCmd_DoctorCommand(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(f)

	assert.Equal(t, "doctor", cmd.Name())
	assert.NotNil(t, cmd.Args)
	assert.NotNil(t, cmd.RunE)
}

func TestRunDoctor_ExitCodeOnFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	opts := &options{
		io:     ios,
		config: f.Config,
		apiClient: func(repoHost string) (*api.Client, error) {
			return nil, fmt.Errorf("not authenticated")
		},
		hostname: "gitlab.com",
	}

	err := runDoctor(t.Context(), opts)
	assert.ErrorIs(t, err, cmdutils.SilentError)
}

func TestMCPSafeAnnotation(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	cmd := NewCmd(cmdtest.NewTestFactory(ios))
	assert.Equal(t, "true", cmd.Annotations[mcpannotations.Safe])
}

func installTestJob(t *testing.T, home, glabPath string) {
	t.Helper()
	units, err := fallbacksync.Units("linux", home, fallbacksync.Job{GlabPath: glabPath, ConfigDir: home})
	require.NoError(t, err)
	for _, u := range units {
		require.NoError(t, os.MkdirAll(filepath.Dir(u.Path), 0o755))
		require.NoError(t, os.WriteFile(u.Path, []byte(u.Content), 0o644))
	}
}

// linuxJobState returns an executor reporting the systemd timer as active or
// not, and the service properties in show.
func linuxJobState(t *testing.T, active bool, show string) *cmdtest.MockExecutor {
	t.Helper()
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	var activeErr error
	if !active {
		activeErr = &exec.ExitError{}
	}
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, activeErr)
	if active {
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "show", "glab-govern-audit-sync.service", "--property=ExecMainStartTimestampMonotonic,ExecMainStatus"}, nil).
			Return([]byte(show), nil)
	}
	return mExec
}

var testBuild = api.BuildInfo{Version: "v1.121.0", Commit: "abc1234"}

var testVersion = fallbacksync.BuildVersion(testBuild)

// healthyLinuxJob returns an executor reporting the systemd timer as loaded
// with a successful last run, and glab as reporting versionOutput.
func healthyLinuxJob(t *testing.T, glab, versionOutput string) cmdutils.Executor {
	t.Helper()
	mExec := linuxJobState(t, true, "ExecMainStartTimestampMonotonic=1234\nExecMainStatus=0\n")
	expectVersion(mExec, glab, versionOutput, nil)
	return mExec
}

func expectVersion(mExec *cmdtest.MockExecutor, glab, versionOutput string, err error) {
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), glab, []string{"version"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
			_, _ = io.WriteString(stdout, versionOutput+"\n")
			return err
		})
}

func TestCheckFallbackSync_VersionDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, "glab 1.100.0 (old1234)"), "linux", testBuild, time.Now())

	assert.False(t, result.ok)
	assert.Equal(t, fmt.Sprintf("the job runs %s, which reports %q, not %q like this glab", glab, "glab 1.100.0 (old1234)", testVersion), result.message)
	assert.Equal(t, "Run: glab govern setup", result.fix)
}

func TestCheckFallbackSync_VersionUnreadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	mExec := linuxJobState(t, true, "ExecMainStartTimestampMonotonic=1234\nExecMainStatus=0\n")
	expectVersion(mExec, glab, "", errors.New("exec format error"))

	result := checkFallbackSync(t.Context(), mExec, "linux", testBuild, time.Now())

	assert.False(t, result.ok)
	assert.Equal(t, fmt.Sprintf("could not run %s version: exec format error", glab), result.message)
	assert.Equal(t, "Run: glab govern setup", result.fix)
}

func TestCheckFallbackSync_NotLoaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	result := checkFallbackSync(t.Context(), linuxJobState(t, false, ""), "linux", testBuild, time.Now())

	assert.False(t, result.ok)
	assert.Equal(t, "the job is installed but not loaded, so it never runs", result.message)
	assert.Equal(t, "Run: glab govern setup", result.fix)
}

func TestCheckFallbackSync_LastRunFailedToStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	// An older glab without --all exits 1 before it can write a status.
	result := checkFallbackSync(t.Context(), linuxJobState(t, true, "ExecMainStartTimestampMonotonic=1234\nExecMainStatus=1\n"), "linux", testBuild, time.Now())

	assert.False(t, result.ok)
	assert.Equal(t, "the job's last run exited with code 1", result.message)
	assert.Contains(t, result.fix, glab+" govern audit sync --all")
}

func TestCheckFallbackSync_NotInstalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	result := checkFallbackSync(t.Context(), nil, "linux", testBuild, time.Now())

	assert.True(t, result.ok, "a machine set up with --no-fallback-sync is healthy")
	assert.Contains(t, result.message, "not installed; sessions sync only through the hooks")
	assert.NotContains(t, result.message, "optional")
}

func TestCheckFallbackSync_Unsupported(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	result := checkFallbackSync(t.Context(), nil, "windows", testBuild, time.Now())

	assert.True(t, result.ok)
	assert.Contains(t, result.message, "not supported on windows")
}

func TestCheckFallbackSync_BinaryMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installTestJob(t, home, "/opt/homebrew/Cellar/glab/1.0.0/bin/glab")

	result := checkFallbackSync(t.Context(), nil, "linux", testBuild, time.Now())

	assert.False(t, result.ok)
	assert.Contains(t, result.message, "/opt/homebrew/Cellar/glab/1.0.0/bin/glab, which no longer exists")
	assert.Equal(t, "Run: glab govern setup", result.fix)
}

func TestCheckFallbackSync_NoRunYet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, time.Now())

	assert.True(t, result.ok)
	assert.Equal(t, fmt.Sprintf("installed (%s); no run recorded yet", glab), result.message)
}

func TestCheckFallbackSync_LastRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{
		FinishedAt:        now.Add(-10 * time.Minute),
		SessionsSynced:    2,
		SessionsCompleted: 1,
		EventsPosted:      9,
		Errors:            []string{"session a: could not resolve project"},
	}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.True(t, result.ok)
	assert.Equal(t,
		fmt.Sprintf("installed (%s); last run 10m0s ago: 2 sessions synced, 1 completed, 9 events posted, 1 error (first: session a: could not resolve project)", glab),
		result.message)
}

func TestCheckFallbackSync_LastRunOnlyFailed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{
		FinishedAt: now.Add(-5 * time.Minute),
		Errors: []string{
			"session a: could not create session: 404 Not Found",
			"session b: could not create session: 404 Not Found",
		},
	}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.False(t, result.ok, "a run where every session failed must not look healthy")
	assert.Equal(t,
		fmt.Sprintf("installed (%s), but the last run synced nothing; last run 5m0s ago: 0 sessions synced, 0 completed, 0 events posted, 2 errors (first: session a: could not create session: 404 Not Found)", glab),
		result.message)
	assert.Equal(t, fmt.Sprintf("Run '%s govern audit sync --all' to see the errors", glab), result.fix)
}

func TestDescribeLastRun_Singular(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	got := describeLastRun(&fallbacksync.Status{
		FinishedAt:     now,
		SessionsSynced: 1,
		EventsPosted:   1,
		Errors:         []string{"session a: boom"},
	}, now)
	assert.Equal(t, "last run 0s ago: 1 session synced, 0 completed, 1 event posted, 1 error (first: session a: boom)", got)
}

func TestDescribeLastRun_EventsTooLarge(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	got := describeLastRun(&fallbacksync.Status{
		FinishedAt:     now,
		SessionsSynced: 1,
		EventsPosted:   19,
		EventsTooLarge: 1,
	}, now)
	assert.Equal(t, "last run 0s ago: 1 session synced, 0 completed, 19 events posted, 1 event too large to post", got)
}

func TestCheckFallbackSync_GovernanceNotEnabledStaysHealthy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{
		FinishedAt:           now,
		GovernanceNotEnabled: []string{"gitlab.com/me/dotfiles", "gitlab.com/gitlab-org/cli"},
	}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.True(t, result.ok, "sessions in projects without governance are expected")
	assert.Contains(t, result.message, "governance not enabled for 2 projects (gitlab.com/me/dotfiles, gitlab.com/gitlab-org/cli), ask a group Owner or administrator to turn it on")
}

func TestCheckFallbackSync_ProjectNotFoundStaysHealthy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{
		FinishedAt:      now,
		ProjectNotFound: []string{"gitlab.com/gitlab-org/cli-docs-test-nonexistent"},
	}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.True(t, result.ok, "sessions in projects the user can't see are expected")
	assert.Contains(t, result.message, "1 project not found or not accessible (gitlab.com/gitlab-org/cli-docs-test-nonexistent)")
}

func TestCheckFallbackSync_NoTokenFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{
		FinishedAt:      now,
		NoToken:         []string{"gitlab.com"},
		ProjectNotFound: []string{"gitlab.com/my-group/private-project"},
	}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.False(t, result.ok, "GitLab needs a token to create sessions and audit events")
	assert.Contains(t, result.message, "the last run had no token for gitlab.com and could not sync some sessions there")
	assert.Contains(t, result.fix, "glab auth login --hostname gitlab.com")
}

func TestCheckFallbackSync_AgentTypeNotSupportedStaysHealthy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{
		FinishedAt:            now,
		AgentTypeNotSupported: []string{"codex on gitlab.example.com"},
		Skipped:               []string{"the Cursor workspace x matches 2 directories (a, b), and its sessions do not show which"},
	}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.True(t, result.ok)
	assert.Contains(t, result.message, "not supported by GitLab yet: codex on gitlab.example.com")
	assert.Contains(t, result.message, "not synced: the Cursor workspace x matches 2 directories")
}

func TestCheckFallbackSync_ShowsDiscoveredAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{FinishedAt: now}))
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex", "cursor"}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.True(t, result.ok)
	assert.True(t, strings.HasSuffix(result.message, "; also syncing codex, cursor sessions"), result.message)
}

func TestCheckFallbackSync_Overdue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GLAB_CONFIG_DIR", home)
	glab, err := os.Executable()
	require.NoError(t, err)
	installTestJob(t, home, glab)

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, fallbacksync.WriteStatus(fallbacksync.Status{
		FinishedAt: now.Add(-3 * time.Hour),
		Paused:     []string{"claude-code: the glab Stop hook is not installed in ~/.claude/settings.json"},
	}))

	result := checkFallbackSync(t.Context(), healthyLinuxJob(t, glab, testVersion), "linux", testBuild, now)

	assert.Contains(t, result.message, "last run 3h0m0s ago (expected every 30 minutes; the job may not be running): 0 sessions synced, 0 completed, 0 events posted; paused for claude-code: the glab Stop hook is not installed")
}

func TestChecks_ReportHomeDirError(t *testing.T) {
	t.Setenv("HOME", "")

	hooks := checkClaudeHooks()
	assert.False(t, hooks.ok)
	assert.Contains(t, hooks.message, "home directory: $HOME is not defined")

	fallback := checkFallbackSync(t.Context(), nil, "linux", testBuild, time.Now())
	assert.False(t, fallback.ok)
	assert.Contains(t, fallback.message, "could not determine home directory: $HOME is not defined")
}

func TestCheckOpenCodePlugin(t *testing.T) {
	// setup points glab and OpenCode at new directories and returns where the
	// plugin goes, with OpenCode chosen in setup when enabled is true.
	setup := func(t *testing.T, enabled bool) string {
		t.Helper()
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
		if enabled {
			require.NoError(t, fallbacksync.SetHookAgents([]string{"opencode"}))
		}
		path, err := opencodeplugin.Path()
		require.NoError(t, err)
		return path
	}

	t.Run("not chosen and OpenCode not found", func(t *testing.T) {
		setup(t, false)
		result := checkOpenCodePlugin()
		assert.True(t, result.ok)
		assert.Equal(t, "OpenCode sessions are not synced", result.message)
	})

	t.Run("not chosen but OpenCode found", func(t *testing.T) {
		setup(t, false)
		dir, err := opencodeplugin.ConfigDir()
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		result := checkOpenCodePlugin()
		assert.True(t, result.ok, "not syncing an agent you did not choose is not a failure")
		assert.Contains(t, result.message, "run 'glab govern setup --agents opencode'")
	})

	t.Run("chosen without the plugin", func(t *testing.T) {
		setup(t, true)
		result := checkOpenCodePlugin()
		assert.False(t, result.ok)
		assert.Contains(t, result.message, "not installed")
		assert.Equal(t, "Run: glab govern setup", result.fix)
	})

	t.Run("plugin installed", func(t *testing.T) {
		path := setup(t, true)
		_, err := opencodeplugin.Install(path)
		require.NoError(t, err)
		result := checkOpenCodePlugin()
		assert.True(t, result.ok)
		assert.Equal(t, "installed in "+opencodeplugin.Display(path), result.message)
	})

	t.Run("plugin out of date", func(t *testing.T) {
		path := setup(t, true)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("// Installed by glab govern setup.\n// an older version\n"), 0o644))
		result := checkOpenCodePlugin()
		assert.False(t, result.ok)
		assert.Contains(t, result.message, "is out of date")
	})

	t.Run("older glab installed the plugin", func(t *testing.T) {
		path := setup(t, false)
		_, err := opencodeplugin.Install(path)
		require.NoError(t, err)
		result := checkOpenCodePlugin()
		assert.True(t, result.ok)
		assert.Contains(t, result.message, "installed in", "an installed plugin counts as set up when nothing is recorded")
	})

	t.Run("unreadable list of agents", func(t *testing.T) {
		setup(t, false)
		path := filepath.Join(os.Getenv("GLAB_CONFIG_DIR"), "gaig", "hook-agents.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
		result := checkOpenCodePlugin()
		assert.False(t, result.ok)
		assert.Contains(t, result.fix, "glab govern setup --uninstall")
	})

	t.Run("file not installed by glab", func(t *testing.T) {
		path := setup(t, true)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("// the user's own plugin\n"), 0o644))
		result := checkOpenCodePlugin()
		assert.False(t, result.ok)
		assert.Contains(t, result.message, "was not installed by glab")
	})
}

func TestCheckClaudeHooks_NotChosen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	require.NoError(t, fallbacksync.SetHookAgents([]string{"opencode"}))

	result := checkClaudeHooks()
	assert.True(t, result.ok, "Claude Code not chosen is not a failure")
	assert.Contains(t, result.message, "Claude Code sessions are not synced")
}

func TestCheckClaudeHooks_UnreadableAgents(t *testing.T) {
	uninstallFix := "Fix it, or run 'glab govern setup --uninstall' to start again"
	tests := map[string]struct {
		file    []string
		wantFix string
	}{
		"Claude Code settings":    {file: []string{".claude", "settings.json"}, wantFix: "Fix it, then run 'glab govern setup' again"},
		"setup's hook agents":     {file: []string{"glab", "gaig", "hook-agents.json"}, wantFix: uninstallFix},
		"setup's discovered list": {file: []string{"glab", "gaig", "discovered-agents.json"}, wantFix: uninstallFix},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
			path := filepath.Join(append([]string{dir}, tc.file...)...)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

			result := checkClaudeHooks()

			assert.False(t, result.ok)
			assert.Contains(t, result.message, path)
			assert.Equal(t, tc.wantFix, result.fix, "uninstalling only helps when setup's own lists are unreadable")
		})
	}
}

func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

// runIsolated runs the tests with the OpenCode plugin under the HOME each test
// sets, and glab's state in a new directory, rather than in the configuration
// of whoever runs the tests.
func runIsolated(m *testing.M) int {
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	dir, err := os.MkdirTemp("", "glab-doctor-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	_ = os.Setenv("GLAB_CONFIG_DIR", dir)
	return m.Run()
}
