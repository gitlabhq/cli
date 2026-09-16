// Package gaig provides shared utilities for GitLab AI Agent Governance (GAIG).
// It handles project ID resolution, machine fingerprint generation, agent identity
// registration, and local caching.
package gaig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/denisbrodbeck/machineid"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
)

const (
	// appKey is used to scope the machine fingerprint to glab GAIG.
	// Changing this will invalidate all cached identities.
	appKey = "glab-gaig-v1"

	// identityCacheTTL is how long a cached identity is considered valid.
	// A 24-hour TTL means a revoked identity may remain usable locally for up
	// to 24 hours after server-side revocation.
	identityCacheTTL = 24 * time.Hour
)

// Identity represents a cached agent identity returned from the GitLab API.
type Identity struct {
	ID        int       `json:"id"`
	AgentType string    `json:"agent_type"`
	RevokedAt *string   `json:"revoked_at"`
	CreatedAt time.Time `json:"created_at"`
	CachedAt  time.Time `json:"cached_at"`
}

// MachineFingerprint returns the GAIG-scoped SHA-256 hash of the machine ID.
// The raw machine ID is never transmitted -- only this hash.
func MachineFingerprint() (string, error) {
	id, err := machineid.ProtectedID(appKey)
	if err != nil {
		// Fallback to hostname when machine-id is unavailable (e.g. CI containers)
		hostname, herr := os.Hostname()
		if herr != nil {
			return "", fmt.Errorf("could not generate machine fingerprint: %w", err)
		}
		id = appKey + ":" + hostname
	}
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:]), nil
}

// ResolveProject resolves the GitLab project from the git remote of the
// current directory. Returns the project and the API client scoped to its host.
func ResolveProject(
	baseRepo func() (glrepo.Interface, error),
	apiClient func(repoHost string) (*api.Client, error),
) (*gitlab.Project, *api.Client, error) {
	repo, err := baseRepo()
	if err != nil {
		return nil, nil, fmt.Errorf("could not resolve git remote: %w", err)
	}

	client, err := apiClient(repo.RepoHost())
	if err != nil {
		return nil, nil, fmt.Errorf("could not create API client: %w", err)
	}

	project, err := api.GetProject(client.Lab(), repo.FullName())
	if err != nil {
		return nil, nil, fmt.Errorf("could not fetch project %s: %w", repo.FullName(), err)
	}

	return project, client, nil
}

// identityCachePath returns the path to the cached identity file for a given
// project ID and agent type.
func identityCachePath(projectID int64, agentType string) (string, error) {
	if strings.ContainsAny(agentType, "/\\.") {
		return "", fmt.Errorf("invalid agent type: %q", agentType)
	}

	filename := fmt.Sprintf("%d_%s.json", projectID, agentType)
	return filepath.Join(config.ConfigDir(), "gaig", "identities", filename), nil
}

// LoadCachedIdentity loads a cached identity for the given project and agent type.
// Returns nil if no valid cache entry exists.
func LoadCachedIdentity(projectID int64, agentType string) (*Identity, error) {
	path, err := identityCachePath(projectID, agentType)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read identity cache: %w", err)
	}

	var identity Identity
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, fmt.Errorf("could not parse identity cache: %w", err)
	}

	// Check TTL
	if time.Since(identity.CachedAt) > identityCacheTTL {
		return nil, nil
	}

	// Check not revoked
	if identity.RevokedAt != nil {
		return nil, nil
	}

	return &identity, nil
}

// SaveCachedIdentity writes an identity to the local cache.
func SaveCachedIdentity(projectID int64, agentType string, identity *Identity) error {
	path, err := identityCachePath(projectID, agentType)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("could not create cache directory: %w", err)
	}

	identity.CachedAt = time.Now()

	data, err := json.MarshalIndent(identity, "", "  ") //nolint:forbidigo // serializing to disk, not stdout
	if err != nil {
		return fmt.Errorf("could not serialise identity: %w", err)
	}

	return os.WriteFile(path, data, 0o600)
}

// RegisterIdentity registers or loads a cached agent identity for the given project.
// It returns (identity, nil) on success, (nil, err) on registration failure,
// and (identity, err) when registration succeeds but the local cache write fails.
func RegisterIdentity(
	client *api.Client,
	projectID int64,
	agentType string,
) (*Identity, error) {
	fingerprint, err := MachineFingerprint()
	if err != nil {
		return nil, err
	}

	// Attempt to load from cache -- silently fall through to API on any error
	if cached, err := LoadCachedIdentity(projectID, agentType); err != nil {
		dbg.Debugf("could not load cached identity: %v", err)
	} else if cached != nil {
		return cached, nil
	}

	type registerRequest struct {
		AgentType          string `json:"agent_type"`
		MachineFingerprint string `json:"machine_fingerprint"`
	}

	type registerResponse struct {
		ID        int     `json:"id"`
		AgentType string  `json:"agent_type"`
		RevokedAt *string `json:"revoked_at"`
	}

	reqBody := registerRequest{
		AgentType:          agentType,
		MachineFingerprint: fingerprint,
	}

	req, err := client.Lab().NewRequest(
		http.MethodPost,
		fmt.Sprintf("projects/%d/ai_agent/identities", projectID),
		reqBody,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("could not create registration request: %w", err)
	}

	var resp registerResponse
	_, err = client.Lab().Do(req, &resp)
	if err != nil {
		return nil, fmt.Errorf("could not register agent identity: %w", err)
	}

	identity := &Identity{
		ID:        resp.ID,
		AgentType: resp.AgentType,
		RevokedAt: resp.RevokedAt,
	}

	// Cache the result
	if err := SaveCachedIdentity(projectID, agentType, identity); err != nil {
		return identity, fmt.Errorf("could not cache identity: %w", err)
	}

	return identity, nil
}
