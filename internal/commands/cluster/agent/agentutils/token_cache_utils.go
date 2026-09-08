package agentutils

import (
	"encoding/base64"
	"fmt"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// CachedToken represents a cached agent token
type CachedToken struct {
	ID        string
	AgentID   int64
	GitLabURL string
	Token     *gitlab.PersonalAccessToken
	Source    string
	FilePath  string // For filesystem tokens (optional)
	Expired   bool   // For list command
	Revoked   bool   // For list command
}

// ParseCacheID parses a cache ID in the format base64(gitlab-url)-agentID
func ParseCacheID(cacheID string) (string, int64, error) {
	// Cache ID format: base64(gitlab-url)-agentID
	// Split on the last hyphen to separate base64 URL from agent ID
	encodedURL, agentIDStr, found := strings.Cut(cacheID, "-")
	if !found {
		return "", 0, fmt.Errorf("invalid cache ID format")
	}

	// Parse the agent ID
	var agentID int64
	if _, err := fmt.Sscanf(agentIDStr, "%d", &agentID); err != nil {
		return "", 0, fmt.Errorf("invalid agent ID in cache ID: %w", err)
	}

	// Decode the base64-encoded GitLab URL
	urlBytes, err := base64.StdEncoding.DecodeString(encodedURL)
	if err != nil {
		return "", 0, fmt.Errorf("failed to decode GitLab URL: %w", err)
	}

	return string(urlBytes), agentID, nil
}

// FilterByAgents filters a slice of cached tokens by agent IDs
func FilterByAgents(tokens []CachedToken, agents []int64) []CachedToken {
	if len(agents) == 0 {
		return tokens
	}

	// Use struct{} to minimize per-entry allocation
	agentSet := make(map[int64]struct{}, len(agents))
	for _, agentID := range agents {
		agentSet[agentID] = struct{}{}
	}

	// Preallocate at most len(tokens); keeps append amortized O(1)
	filtered := make([]CachedToken, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := agentSet[token.AgentID]; ok {
			filtered = append(filtered, token)
		}
	}

	return filtered
}
