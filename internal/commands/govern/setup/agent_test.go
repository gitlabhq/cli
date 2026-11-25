//go:build !integration

package setup

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestSetup_RefusesCodingAgents(t *testing.T) {
	tests := []struct {
		name         string
		args         string
		runningAgent string
		codingAgent  string
		tty          bool
		wantErr      string
		wantOut      string
	}{
		{
			name:         "setup with --yes from an agent",
			args:         "--yes --no-fallback-sync",
			runningAgent: "claude-code",
			wantErr:      "glab minder setup must be confirmed by a person, and a coding agent (claude-code) is running glab. Ask the user to run 'glab minder setup' in their own terminal",
		},
		{
			name:         "uninstall with --yes from an agent",
			args:         "--uninstall --yes",
			runningAgent: "codex",
			wantErr:      "glab minder setup --uninstall must be confirmed by a person, and a coding agent (codex) is running glab. Ask the user to run 'glab minder setup --uninstall' in their own terminal",
		},
		{
			name:         "setup without --yes from an agent with no terminal",
			args:         "--no-fallback-sync",
			runningAgent: "cursor",
			wantErr:      "a coding agent (cursor) is running glab",
		},
		{
			name:         "setup without --yes from an agent with a terminal",
			args:         "--no-fallback-sync",
			tty:          true,
			runningAgent: "zed",
			wantErr:      "a coding agent (zed) is running glab",
		},
		{
			name:         "uninstall without --yes from an agent with a terminal",
			args:         "--uninstall",
			tty:          true,
			runningAgent: "opencode",
			wantErr:      "glab minder setup --uninstall must be confirmed by a person, and a coding agent (opencode) is running glab",
		},
		{
			name:         "uninstall with --yes from an unnamed agent",
			args:         "--uninstall --yes",
			runningAgent: "unknown",
			wantErr:      "a coding agent (unknown) is running glab",
		},
		{
			name:    "setup with --yes and no agent",
			args:    "--yes --no-fallback-sync",
			wantOut: "Hooks installed",
		},
		{
			name:        "setup with --yes in an IDE terminal",
			args:        "--yes --no-fallback-sync",
			codingAgent: "cursor-terminal",
			wantOut:     "Hooks installed",
		},
		{
			name:    "uninstall with --yes and no agent",
			args:    "--uninstall --yes",
			wantOut: "Hooks removed",
		},
		{
			name:        "uninstall with --yes in an IDE terminal",
			args:        "--uninstall --yes",
			codingAgent: "zed-terminal",
			wantOut:     "Hooks removed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
			settings := filepath.Join(dir, ".claude", "settings.json")
			uninstall := strings.Contains(tt.args, "--uninstall")
			if uninstall {
				require.NoError(t, installClaudeHooks(&options{settingsPathOverride: settings}))
			}

			mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
			if tt.wantOut != "" {
				mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, &exec.ExitError{}).AnyTimes()
			}
			run := cmdtest.SetupCmdForTest(t, NewCmd, tt.tty,
				cmdtest.WithExecutor(mExec),
				cmdtest.WithBuildInfo(api.BuildInfo{Version: "v1.121.0", CodingAgent: tt.codingAgent, RunningAgent: tt.runningAgent}),
			)

			out, err := run(tt.args)

			installed, hooksErr := claudeHooksInstalled(&options{settingsPathOverride: settings})
			require.NoError(t, hooksErr)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Equal(t, uninstall, installed, "a refused run leaves the hooks as they were")
				return
			}
			require.NoError(t, err)
			assert.Contains(t, out.String(), tt.wantOut)
			assert.Equal(t, !uninstall, installed)
		})
	}
}
