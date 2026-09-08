package agentutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func TestParseCacheID_Valid(t *testing.T) {
	// base64("https://gitlab.com") = aHR0cHM6Ly9naXRsYWIuY29t
	cacheID := "aHR0cHM6Ly9naXRsYWIuY29t-123"

	url, agentID, err := ParseCacheID(cacheID)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.com", url)
	assert.Equal(t, int64(123), agentID)
}

func TestParseCacheID_Invalid_Format(t *testing.T) {
	_, _, err := ParseCacheID("invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cache ID format")
}

func TestParseCacheID_Invalid_AgentID(t *testing.T) {
	// still a valid base64 but agent part is not numeric
	cacheID := "aHR0cHM6Ly9naXRsYWIuY29t-abc"
	_, _, err := ParseCacheID(cacheID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid agent ID")
}

func TestFilterByAgents_Subset(t *testing.T) {
	tokens := []CachedToken{
		{AgentID: 1, Token: &gitlab.PersonalAccessToken{}},
		{AgentID: 2, Token: &gitlab.PersonalAccessToken{}},
		{AgentID: 3, Token: &gitlab.PersonalAccessToken{}},
		{AgentID: 4, Token: &gitlab.PersonalAccessToken{}},
	}

	filtered := FilterByAgents(tokens, []int64{1, 3})
	require.Len(t, filtered, 2)
	assert.Equal(t, int64(1), filtered[0].AgentID)
	assert.Equal(t, int64(3), filtered[1].AgentID)
}

func TestFilterByAgents_NoFilter(t *testing.T) {
	tokens := []CachedToken{
		{AgentID: 1, Token: &gitlab.PersonalAccessToken{}},
		{AgentID: 2, Token: &gitlab.PersonalAccessToken{}},
	}

	filtered := FilterByAgents(tokens, nil)
	// returns original slice when no filter
	require.Len(t, filtered, 2)
	assert.Equal(t, tokens, filtered)
}

func TestFilterByAgents_EmptyTokens(t *testing.T) {
	var tokens []CachedToken
	filtered := FilterByAgents(tokens, []int64{1})
	require.Empty(t, filtered)
}
