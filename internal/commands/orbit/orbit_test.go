//go:build !integration

package orbit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmd_PassthroughStructure(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	cmd := NewCmd(f)

	assert.Equal(t, "orbit", cmd.Name())
	assert.True(t, cmd.DisableFlagParsing)
	assert.Empty(t, cmd.Annotations["mcp:safe"])
	assert.NotNil(t, cmd.Flags().Lookup("install"))
	assert.NotNil(t, cmd.Flags().Lookup("update"))
	yesFlag := cmd.Flags().Lookup("yes")
	require.NotNil(t, yesFlag)
	assert.Equal(t, "y", yesFlag.Shorthand)
}

func TestSplitGlabFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		wantYes       bool
		wantInstall   bool
		wantUpdate    bool
		wantShowHelp  bool
		wantForwarded []string
	}{
		{
			name:          "verbatim forwarding of remote command",
			args:          []string{"remote", "query", "-"},
			wantForwarded: []string{"remote", "query", "-"},
		},
		{
			name:          "verbatim forwarding of local command",
			args:          []string{"local", "sql", "SELECT 1"},
			wantForwarded: []string{"local", "sql", "SELECT 1"},
		},
		{
			name:          "verbatim forwarding of top-level command",
			args:          []string{"version"},
			wantForwarded: []string{"version"},
		},
		{
			name:          "yes long flag is consumed and dropped",
			args:          []string{"--yes", "local", "index"},
			wantYes:       true,
			wantForwarded: []string{"local", "index"},
		},
		{
			name:          "yes short flag is consumed and dropped",
			args:          []string{"-y", "remote", "query", "-"},
			wantYes:       true,
			wantForwarded: []string{"remote", "query", "-"},
		},
		{
			name:        "install flag is consumed and dropped",
			args:        []string{"--install", "--yes"},
			wantYes:     true,
			wantInstall: true,
		},
		{
			name:       "update flag is consumed and dropped",
			args:       []string{"--update"},
			wantUpdate: true,
		},
		{
			name:         "no args shows glab help",
			wantShowHelp: true,
		},
		{
			name:         "lone help shows glab help",
			args:         []string{"--help"},
			wantShowHelp: true,
		},
		{
			name:         "lone short help shows glab help",
			args:         []string{"-h"},
			wantShowHelp: true,
		},
		{
			name:          "help with a forwarded command is forwarded to the binary",
			args:          []string{"remote", "--help"},
			wantForwarded: []string{"remote", "--help"},
		},
		{
			name:          "glab flag after the subcommand is forwarded to the binary",
			args:          []string{"setup", "claude", "--yes"},
			wantForwarded: []string{"setup", "claude", "--yes"},
		},
		{
			name:          "double dash forwards the rest verbatim",
			args:          []string{"--", "--yes", "remote"},
			wantForwarded: []string{"--yes", "remote"},
		},
		{
			name:         "help before a command still shows glab help",
			args:         []string{"--help", "remote"},
			wantShowHelp: true,
		},
		{
			name:          "unknown leading flag forwards everything to the binary",
			args:          []string{"--log-level", "debug", "remote", "status"},
			wantForwarded: []string{"--log-level", "debug", "remote", "status"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flags := splitGlabFlags(tt.args)
			assert.Equal(t, tt.wantYes, flags.yes)
			assert.Equal(t, tt.wantInstall, flags.install)
			assert.Equal(t, tt.wantUpdate, flags.update)
			assert.Equal(t, tt.wantShowHelp, flags.showHelp)
			assert.Equal(t, tt.wantForwarded, flags.forwarded)
		})
	}
}

func TestNewPassthroughRunner_InstallUpdateMutuallyExclusive(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	_, _, err := newPassthroughRunner(f, []string{"--install", "--update"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestNewPassthroughRunner_InstallWithCommandErrors(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	_, _, err := newPassthroughRunner(f, []string{"--update", "remote", "status"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined with a command")
}

func TestOrbitCredentialEnv_InjectsResolvedCredential(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "glpat-remote")

	client, err := api.NewClientFromConfig("gitlab.com", config.NewBlankConfig(), false, "test-agent")
	require.NoError(t, err)

	f := cmdtest.NewTestFactory(nil, cmdtest.WithApiClient(client))

	env := orbitCredentialEnv(t.Context(), f)
	assert.Contains(t, env, "ORBIT_API_BASE_URL=https://gitlab.com")
	assert.Contains(t, env, "ORBIT_AUTH_HEADER_NAME=Private-Token")
	assert.Contains(t, env, "ORBIT_AUTH_HEADER_VALUE=glpat-remote")
}

func TestOrbitCredentialEnv_SkipsWhenUnauthenticated(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	client, err := api.NewClientFromConfig("gitlab.com", config.NewBlankConfig(), false, "test-agent")
	require.NoError(t, err)

	ios, _, _, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithApiClient(client))

	assert.Nil(t, orbitCredentialEnv(t.Context(), f))
	assert.Empty(t, stderr.String())
}
