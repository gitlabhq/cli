package clear

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/v3/testing"

	agentutils "gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/agentutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestOptions_filterByAgents(t *testing.T) {
	opts := &options{
		agents: []int64{1, 3},
	}

	tokens := []agentutils.CachedToken{
		{AgentID: 1},
		{AgentID: 2},
		{AgentID: 3},
		{AgentID: 4},
	}

	filtered := agentutils.FilterByAgents(tokens, opts.agents)
	require.Len(t, filtered, 2)
	assert.Equal(t, int64(1), filtered[0].AgentID)
	assert.Equal(t, int64(3), filtered[1].AgentID)
}

func TestOptions_filterByAgents_noFilter(t *testing.T) {
	opts := &options{
		agents: []int64{},
	}

	tokens := []agentutils.CachedToken{
		{AgentID: 1},
		{AgentID: 2},
	}

	filtered := agentutils.FilterByAgents(tokens, opts.agents)
	require.Len(t, filtered, 2) // Should return all tokens when no filter
}

func TestOptions_parseCacheID(t *testing.T) {
	// Test valid cache ID
	// base64("https://gitlab.com") = aHR0cHM6Ly9naXRsYWIuY29t
	cacheID := "aHR0cHM6Ly9naXRsYWIuY29t-123"

	gitlabURL, agentID, err := agentutils.ParseCacheID(cacheID)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.com", gitlabURL)
	assert.Equal(t, int64(123), agentID)
}

func TestOptions_parseCacheID_invalid(t *testing.T) {
	// Test invalid cache ID format
	_, _, err := agentutils.ParseCacheID("invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cache ID format")
}

func TestClearNoTokens(t *testing.T) {
	// GIVEN
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	// WHEN
	output, err := exec("--filesystem --keyring=false")
	require.NoError(t, err)

	// THEN
	assert.Equal(t, heredoc.Doc(`
		No cached tokens found to clear.
	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestClearValidation_NoSources(t *testing.T) {
	// GIVEN
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	// WHEN
	_, err := exec("--filesystem=false --keyring=false")

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one cache source must be enabled")
}
