package docker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/docker/docker-credential-helpers/credentials"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/oauth2"
)

var _ credentials.Helper = (*Helper)(nil)

// errNoHostnameForDomain is the shared "no host lists this domain" signal
// findHostnameByDomain returns. Callers translate it into their own
// domain-specific error or sentinel.
var errNoHostnameForDomain = errors.New("no hostname associated with domain")

// artifactRegistryTimeout bounds the artifact-registry token exchange Get
// attempts before falling back to (or failing with) the container-registry
// path. An instance without Artifact Registry answers 403/404 in
// milliseconds; this is only spent when the server accepts the connection
// and never answers.
const artifactRegistryTimeout = 30 * time.Second

// Helper implements a docker-credential-* helper interface.
// It assists in logging into a registry.
type Helper struct {
	client *http.Client
	cfg    config.Config
	io     *iostreams.IOStreams
	// userAgent is used only when building the artifact-registry API client,
	// since that client is built fresh per-host rather than reused from the
	// caller's default-host client.
	userAgent string
	// ctx is the command's context, so a cancellation or deadline set by the
	// caller (rather than a fresh, unrelated context.Background()) governs the
	// artifact-registry exchange. Get is a fixed third-party interface method
	// with no context parameter of its own, so this is the only way to thread
	// one through. A zero-value Helper (as constructed by tests that only
	// exercise config-driven paths) falls back to context.Background().
	ctx context.Context
}

// Get fetches the glab auth token for registryURL. It tries the Artifact
// Registry first; on any failure, it falls back to the container-registry
// path, using that credential and logging the fall-through if the fallback
// succeeds. If neither produces a credential, it prefers the artifact
// registry error whenever the domain was configured for it, since that
// names the actual failure, where the container-registry path can only say
// the domain is unassociated.
func (h *Helper) Get(registryURL string) (string, string, error) {
	base := h.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, artifactRegistryTimeout)
	defer cancel()

	arUser, arToken, arErr := h.getArtifactRegistryToken(ctx, registryURL)
	if arErr == nil {
		return arUser, arToken, nil
	}

	crUser, crToken, crErr := h.getContainerRegistryToken(registryURL)
	if crErr == nil {
		// LogErrorf, not dbg.Debugf: Docker forwards the helper's stderr, so
		// this is the only way a broken artifact-registry config (silently
		// degrading to the container-registry credential on every pull) is
		// visible without DEBUG=1.
		if !errors.Is(arErr, errNotArtifactRegistry) {
			h.io.LogErrorf("%s artifact registry exchange failed for %s, falling back to container registry credentials: %v\n",
				h.io.Color().WarnIcon(), registryURL, arErr)
		}
		return crUser, crToken, nil
	}

	if !errors.Is(arErr, errNotArtifactRegistry) {
		// crErr is joined in when it names a real container-registry failure
		// (for example, a dual-listed domain whose stored credentials are
		// empty), so that failure is not silently swallowed by arErr alone.
		// A domain simply unassociated with container_registry_domains carries
		// no such signal, so it is left out: arErr already names the actual
		// exchange failure, and "no hostname associated" would read as if the
		// domain were unknown rather than misconfigured for artifact registry.
		if !errors.Is(crErr, errNoHostnameForDomain) {
			return "", "", errors.Join(arErr, crErr)
		}
		return "", "", arErr
	}
	return "", "", crErr
}

// getContainerRegistryToken fetches the glab auth token for registryURL from
// the container-registry path: it reads the stored user/token for the
// associated host directly, without minting anything new.
func (h *Helper) getContainerRegistryToken(registryURL string) (string, string, error) {
	hostname, err := h.findAssociatedHostname(registryURL)
	if err != nil {
		return "", "", err
	}

	// searchEnvForIdentity: false. This helper runs as a Docker subprocess
	// that inherits the user's shell environment, so a stray GITLAB_TOKEN must
	// not decide which identity's token gets refreshed here.
	ts, err := oauth2.NewConfigTokenSource(h.cfg, h.client, glinstance.DefaultProtocol, hostname, false)
	if err != nil {
		return "", "", fmt.Errorf("failed to get OAuth2 token source to potentially refresh token: %w", err)
	}
	if _, err := ts.Token(); err != nil {
		return "", "", fmt.Errorf("refreshing oauth2 token: %w", err)
	}

	// ts.Token() persists a refreshed token but does not mutate our in-memory
	// config, so re-read it to pick up a rotated token. For a file-backed host the
	// in-memory copy would otherwise still hold the pre-refresh (now expired)
	// token; a keyring-backed host reads live, but reloading is correct for both.
	// We deliberately do not use the token ts.Token() returns: it is derived with
	// env-var lookup enabled, and GITLAB_TOKEN must not override the per-host
	// credential here.
	//
	// A config that cannot be re-read is not fatal (for example, the config lives
	// in an XDG_CONFIG_DIRS entry and no user-level file exists yet, so no refresh
	// ever created one); fall back to the in-memory copy, matching freshest().
	cfg := h.cfg
	if fresh, err := h.cfg.Reload(); err == nil {
		cfg = fresh
	} else {
		dbg.Debugf("docker helper: could not re-read config for %q, using in-memory copy: %v", hostname, err)
	}

	user, err := cfg.Get(hostname, "user")
	if err != nil {
		return "", "", err
	}

	// Skip env var lookup: GITLAB_TOKEN should not override per-host credentials
	// stored in config when acting as a Docker credential helper.
	token, _, err := cfg.GetWithSource(hostname, "token", false)
	if err != nil {
		return "", "", err
	}

	if user == "" {
		return "", "", fmt.Errorf("glab user for this registryURL (hostname) is empty")
	}

	if token == "" {
		return "", "", fmt.Errorf("glab token for this registryURL (hostname) is empty")
	}

	return user, token, nil
}

// findAssociatedHostname takes a GitLab container registry URL
// and finds its associated GitLab instance's hostname.
//
// The returned error wraps errNoHostnameForDomain when the domain is simply
// unassociated, so Get can tell that case apart from a real container-registry
// failure worth surfacing alongside an artifact-registry error.
func (h *Helper) findAssociatedHostname(registryURL string) (string, error) {
	hostname, err := h.findHostnameByDomain("container_registry_domains", registryURL)
	if errors.Is(err, errNoHostnameForDomain) {
		return "", fmt.Errorf("no hostname associated with registryURL: %s: %w", registryURL, errNoHostnameForDomain)
	}
	return hostname, err
}

// findHostnameByDomain scans every host's domainsConfigKey for registryURL,
// returning the first host that lists it.
//
// A key that is simply not set reads back as "" with no error, so an error
// from GetWithSource is a real read failure: structurally broken YAML for
// that host, or a locked or denied keyring. Such an error must not be
// allowed to look like "this host doesn't list the domain", which would
// report configuration that exists but couldn't be read as missing.
//
// The errors are collected rather than returned on the spot: a broken entry
// for one host must not stop a different host from legitimately resolving
// the domain. A read failure can only have changed the answer when no host
// matched, so that is where it is reported.
func (h *Helper) findHostnameByDomain(domainsConfigKey, registryURL string) (string, error) {
	hostnames, err := h.cfg.Hosts()
	if err != nil {
		return "", err
	}

	var readErr error

	for _, hostname := range hostnames {
		domains, err := readDomains(h.cfg, hostname, domainsConfigKey)
		if err != nil {
			readErr = errors.Join(readErr, err)
			continue
		}
		if slices.Contains(domains, registryURL) {
			if readErr != nil {
				h.io.LogErrorf("%s another host's configuration could not be read; if it also handles %s, glab may be using the wrong GitLab instance's credentials: %v\n",
					h.io.Color().WarnIcon(), registryURL, readErr)
			}
			return hostname, nil
		}
	}

	if readErr != nil {
		return "", readErr
	}

	return "", errNoHostnameForDomain
}

type helperError struct {
	action    string
	serverURL string
}

func (e helperError) Error() string {
	msg := "glab auth docker-helper does not support manual registry " + e.action + "s. "
	msg += "Remove the glab credential helper for " + e.serverURL + " "
	msg += "if you'd like to manually handle credentials for this registry."
	return msg
}

func (h *Helper) Add(creds *credentials.Credentials) error {
	return helperError{action: "login", serverURL: creds.ServerURL}
}

func (h *Helper) Delete(serverURL string) error {
	return helperError{action: "logout", serverURL: serverURL}
}

func (h *Helper) List() (map[string]string, error) {
	return nil, errors.New("glab auth docker-helper does not support listing registries")
}

// readDomains reads domainsConfigKey for hostname and parses it into a
// domain slice.
//
// A key that is simply not set reads back as "" with no error, so an error
// here is a real read failure: structurally broken YAML for the host, or a
// locked or denied keyring. Callers must not let that look like "this host
// doesn't list the domain," which would report configuration that exists
// but couldn't be read as missing.
func readDomains(cfg config.Config, hostname, domainsConfigKey string) ([]string, error) {
	domains, _, err := cfg.GetWithSource(hostname, domainsConfigKey, false)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s for %q: %w", domainsConfigKey, hostname, err)
	}
	return config.ParseDomains(domains), nil
}
