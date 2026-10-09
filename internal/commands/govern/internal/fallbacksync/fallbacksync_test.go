//go:build !integration

package fallbacksync

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestUnits_Darwin(t *testing.T) {
	t.Parallel()

	units, err := Units("darwin", "/home/me", Job{GlabPath: "/opt/homebrew/bin/glab", ConfigDir: "/home/me/.config/glab-cli"})
	require.NoError(t, err)
	require.Len(t, units, 1)

	assert.Equal(t, "/home/me/Library/LaunchAgents/com.gitlab.glab-govern-audit-sync.plist", units[0].Path)
	assert.Equal(t, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.gitlab.glab-govern-audit-sync</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/homebrew/bin/glab</string>
        <string>govern</string>
        <string>audit</string>
        <string>sync</string>
        <string>--all</string>
        <string>--silent</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>GLAB_CONFIG_DIR</key>
        <string>/home/me/.config/glab-cli</string>
    </dict>
    <key>StartInterval</key>
    <integer>1800</integer>
    <key>RunAtLoad</key>
    <false/>
</dict>
</plist>
`, units[0].Content)
}

func TestUnits_Linux(t *testing.T) {
	t.Parallel()

	units, err := Units("linux", "/home/me", Job{GlabPath: "/home/me/.local/share/mise/shims/glab", ConfigDir: "/home/me/.config/glab-cli"})
	require.NoError(t, err)
	require.Len(t, units, 2)

	assert.Equal(t, "/home/me/.config/systemd/user/glab-govern-audit-sync.service", units[0].Path)
	assert.Equal(t, `[Unit]
Description=GitLab AI agent governance fallback sync

[Service]
Type=oneshot
Environment="GLAB_CONFIG_DIR=/home/me/.config/glab-cli"
ExecStart="/home/me/.local/share/mise/shims/glab" govern audit sync --all --silent
`, units[0].Content)

	assert.Equal(t, "/home/me/.config/systemd/user/glab-govern-audit-sync.timer", units[1].Path)
	assert.Equal(t, `[Unit]
Description=GitLab AI agent governance fallback sync timer

[Timer]
OnBootSec=5min
OnUnitActiveSec=30min

[Install]
WantedBy=timers.target
`, units[1].Content)
}

func TestUnits_Unsupported(t *testing.T) {
	t.Parallel()

	_, err := Units("windows", `C:\Users\me`, Job{GlabPath: `C:\glab.exe`})
	require.ErrorIs(t, err, ErrUnsupportedOS)
}

func TestInstalledBinary_RoundTrip(t *testing.T) {
	t.Parallel()

	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			glab := `/odd path/100%/a&b/"q"\x/glab`

			units, err := Units(goos, home, Job{GlabPath: glab, ConfigDir: `/cfg/50%/"x"`})
			require.NoError(t, err)
			for _, u := range units {
				require.NoError(t, os.MkdirAll(filepath.Dir(u.Path), 0o755))
				require.NoError(t, os.WriteFile(u.Path, []byte(u.Content), 0o644))
			}

			got, err := InstalledBinary(goos, home)
			require.NoError(t, err)
			assert.Equal(t, glab, got)
		})
	}
}

func TestInstalledBinary_NotInstalled(t *testing.T) {
	t.Parallel()

	_, err := InstalledBinary("linux", t.TempDir())
	require.ErrorIs(t, err, ErrNotInstalled)
}

func TestInstallUninstall_Darwin(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.gitlab.glab-govern-audit-sync.plist")
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))

	gomock.InOrder(
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "launchctl", []string{"bootout", "gui/501/com.gitlab.glab-govern-audit-sync"}, nil).
			Return([]byte("not loaded"), errors.New("exit status 3")),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "launchctl", []string{"bootstrap", "gui/501", plist}, nil).
			Return(nil, nil),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "launchctl", []string{"bootout", "gui/501/com.gitlab.glab-govern-audit-sync"}, nil).
			Return(nil, nil),
	)

	require.NoError(t, Install(t.Context(), mExec, "darwin", home, Job{GlabPath: "/usr/local/bin/glab"}, 501))
	assert.FileExists(t, plist)

	removed, err := Uninstall(t.Context(), mExec, "darwin", home, 501)
	require.NoError(t, err)
	assert.True(t, removed)
	assert.NoFileExists(t, plist)
}

func TestInstallUninstall_Linux(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dir := filepath.Join(home, ".config", "systemd", "user")
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))

	gomock.InOrder(
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "daemon-reload"}, nil).
			Return(nil, nil),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "enable", "--now", "glab-govern-audit-sync.timer"}, nil).
			Return(nil, nil),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "disable", "--now", "glab-govern-audit-sync.timer"}, nil).
			Return(nil, nil),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "daemon-reload"}, nil).
			Return(nil, nil),
	)

	require.NoError(t, Install(t.Context(), mExec, "linux", home, Job{GlabPath: "/usr/bin/glab"}, 1000))
	assert.FileExists(t, filepath.Join(dir, "glab-govern-audit-sync.service"))
	assert.FileExists(t, filepath.Join(dir, "glab-govern-audit-sync.timer"))

	removed, err := Uninstall(t.Context(), mExec, "linux", home, 1000)
	require.NoError(t, err)
	assert.True(t, removed)
	assert.NoFileExists(t, filepath.Join(dir, "glab-govern-audit-sync.service"))
	assert.NoFileExists(t, filepath.Join(dir, "glab-govern-audit-sync.timer"))
}

func TestInstall_LoadFailure(t *testing.T) {
	t.Parallel()

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "daemon-reload"}, nil).
		Return([]byte("Failed to connect to bus"), errors.New("exit status 1"))

	home := t.TempDir()
	err := Install(t.Context(), mExec, "linux", home, Job{GlabPath: "/usr/bin/glab"}, 1000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to connect to bus")

	_, err = InstalledBinary("linux", home)
	require.ErrorIs(t, err, ErrNotInstalled, "a job that could not be loaded is not left installed")
}

func TestUninstall_NothingInstalled(t *testing.T) {
	t.Parallel()

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "disable", "--now", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, errors.New("unit not found"))

	removed, err := Uninstall(t.Context(), mExec, "linux", t.TempDir(), 1000)
	require.NoError(t, err)
	assert.False(t, removed)
}

func TestStatus_RoundTrip(t *testing.T) {
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())

	_, err := ReadStatus()
	require.ErrorIs(t, err, ErrNoStatus)

	want := Status{
		StartedAt:      time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC),
		FinishedAt:     time.Date(2026, 10, 8, 1, 0, 2, 0, time.UTC),
		SessionsSynced: 2,
		EventsPosted:   5,
		Errors:         []string{"session a: boom"},
	}
	require.NoError(t, WriteStatus(want))

	got, err := ReadStatus()
	require.NoError(t, err)
	assert.Equal(t, want, *got)
}

func TestState_Darwin(t *testing.T) {
	t.Parallel()

	launchctlPrint := func(t *testing.T, out string, err error) JobState {
		t.Helper()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "launchctl", []string{"print", "gui/501/com.gitlab.glab-govern-audit-sync"}, nil).
			Return([]byte(out), err)
		state, stateErr := State(t.Context(), mExec, "darwin", 501)
		require.NoError(t, stateErr)
		return state
	}

	t.Run("failed run", func(t *testing.T) {
		t.Parallel()
		state := launchctlPrint(t, "gui/501/com.gitlab.glab-govern-audit-sync = {\n\texit timeout = 5\n\truns = 1\n\tlast exit code = 1\n}\n", nil)
		assert.Equal(t, JobState{Loaded: true, Ran: true, ExitCode: 1}, state)
	})

	t.Run("never run", func(t *testing.T) {
		t.Parallel()
		state := launchctlPrint(t, "gui/501/com.gitlab.glab-govern-audit-sync = {\n\truns = 0\n\tlast exit code = (never exited)\n}\n", nil)
		assert.Equal(t, JobState{Loaded: true}, state)
	})

	t.Run("not loaded", func(t *testing.T) {
		t.Parallel()
		state := launchctlPrint(t, "Could not find service \"com.gitlab.glab-govern-audit-sync\" in domain for user gui: 501\n", &exec.ExitError{})
		assert.Equal(t, JobState{}, state)
	})

	t.Run("launchctl does not run", func(t *testing.T) {
		t.Parallel()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "launchctl", gomock.Any(), nil).Return(nil, exec.ErrNotFound)
		_, err := State(t.Context(), mExec, "darwin", 501)
		require.ErrorIs(t, err, exec.ErrNotFound)
	})
}

func TestState_Linux(t *testing.T) {
	t.Parallel()

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	gomock.InOrder(
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
			Return(nil, nil),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "show", "glab-govern-audit-sync.service", "--property=ExecMainStartTimestampMonotonic,ExecMainStatus"}, nil).
			Return([]byte("ExecMainStartTimestampMonotonic=0\nExecMainStatus=0\n"), nil),
	)

	state, err := State(t.Context(), mExec, "linux", 1000)
	require.NoError(t, err)
	assert.Equal(t, JobState{Loaded: true}, state, "a start timestamp of 0 means the service has not run since boot")
}
