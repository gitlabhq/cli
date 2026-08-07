package docker

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/docker/docker-credential-helpers/credentials"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/oauth2"
)

var _ credentials.Helper = (*Helper)(nil)

// Helper implements a docker-credential-* helper interface.
// It assists in logging into a registry.
type Helper struct {
	client *http.Client
	cfg    config.Config
	io     *iostreams.IOStreams
}

// Get fetches the glab auth token for the given registryURL.
func (h *Helper) Get(registryURL string) (string, string, error) {
	hostname, err := h.findAssociatedHostname(registryURL)
	if err != nil {
		return "", "", err
	}

	ts, err := oauth2.NewConfigTokenSource(h.cfg, h.client, glinstance.DefaultProtocol, hostname)
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
func (h *Helper) findAssociatedHostname(registryURL string) (string, error) {
	hostnames, err := h.cfg.Hosts()
	if err != nil {
		return "", err
	}

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
	var readErr error

	for _, hostname := range hostnames {
		domains, err := readDomains(h.cfg, hostname)
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

	return "", fmt.Errorf("no hostname associated with registryURL: %s", registryURL)
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

// readDomains reads container_registry_domains for hostname and parses it
// into a domain slice.
//
// A key that is simply not set reads back as "" with no error, so an error
// here is a real read failure: structurally broken YAML for the host, or a
// locked or denied keyring. Callers must not let that look like "this host
// doesn't list the domain," which would report configuration that exists
// but couldn't be read as missing.
func readDomains(cfg config.Config, hostname string) ([]string, error) {
	domains, _, err := cfg.GetWithSource(hostname, "container_registry_domains", false)
	if err != nil {
		return nil, fmt.Errorf("failed to read container_registry_domains for %q: %w", hostname, err)
	}
	return parseDomains(domains), nil
}

func parseDomains(domains string) []string {
	if domains == "" {
		return nil
	}
	var result []string
	for domain := range strings.SplitSeq(domains, ",") {
		if domain = strings.TrimSpace(domain); domain != "" {
			result = append(result, domain)
		}
	}
	return result
}
