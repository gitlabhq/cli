package docker

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/api/artifactregistry"
)

// errNotArtifactRegistry signals that registryURL is not configured under
// artifact_registry_domains for any host: the fall-through case, not a
// failure worth reporting on its own.
var errNotArtifactRegistry = errors.New("registryURL is not configured for artifact registry")

// findArtifactRegistryHostname takes a registry URL and finds its associated
// GitLab instance's hostname among hosts that list it under
// artifact_registry_domains.
func (h *Helper) findArtifactRegistryHostname(registryURL string) (string, error) {
	hostname, err := h.findHostnameByDomain("artifact_registry_domains", registryURL)
	if errors.Is(err, errNoHostnameForDomain) {
		return "", errNotArtifactRegistry
	}
	return hostname, err
}

// getArtifactRegistryToken exchanges the caller's GitLab credential for a
// short-lived Artifact Registry access token scoped to registryURL's
// instance, returning it as a (username, secret) pair for basic auth, the
// same way the container-registry path returns its own credentials.
func (h *Helper) getArtifactRegistryToken(ctx context.Context, registryURL string) (string, string, error) {
	hostname, err := h.findArtifactRegistryHostname(registryURL)
	if err != nil {
		return "", "", err
	}

	// WithoutTokenFromEnvironment: Docker runs this helper as a subprocess
	// that inherits the user's shell environment, so a stray
	// GITLAB_TOKEN/GITLAB_ACCESS_TOKEN must not decide which identity mints
	// the artifact-registry token.
	apiClient, err := api.NewClientFromConfig(hostname, h.cfg, false, h.userAgent, api.WithoutTokenFromEnvironment())
	if err != nil {
		return "", "", fmt.Errorf("failed to build artifact registry API client for %q: %w", hostname, err)
	}

	result, err := artifactregistry.NewClient(apiClient.Lab()).ExchangeToken(ctx, artifactregistry.DockerHelperDuration)
	if err != nil {
		return "", "", fmt.Errorf("failed to get artifact registry token: %w", err)
	}

	// "__token__" is a placeholder username the Artifact Registry ignores; it
	// only checks the secret. Do not change this to "<token>": that exact
	// string is Docker's own sentinel (see docker/cli's tokenUsername) for
	// "treat the secret as an OAuth2 IdentityToken and refresh it through the
	// registry's separate token endpoint" — a different auth flow than the
	// basic auth this secret is meant for, and returning it here would break
	// the exchange.
	return "__token__", result.Token, nil
}
