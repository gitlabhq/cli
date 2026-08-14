package docker

import (
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dockercredhelper"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

func configureDocker(iostreams *iostreams.IOStreams, cfg config.Config) error {
	if _, err := dockercredhelper.Install(); err != nil {
		return err
	}

	hostnames, err := cfg.Hosts()
	if err != nil {
		return fmt.Errorf("fetching list of hosts handled by glab: %w", err)
	}

	var configuredDomains []string
	var readErr error
	for _, hostname := range hostnames {
		// Report a read failure and carry on so the remaining hosts are still
		// configured, but keep it to decide the exit below: "no domains are
		// configured" and "your domains could not be read" need different
		// messages, and a partial failure must not be reported as full success.
		domains, err := readDomains(cfg, hostname, "container_registry_domains")
		if err != nil {
			readErr = errors.Join(readErr, err)
			iostreams.LogErrorf("%s Skipped %s: %v\n", iostreams.Color().WarnIcon(), hostname, err)
			continue
		}
		configuredDomains = append(configuredDomains, domains...)
	}

	if len(configuredDomains) == 0 {
		// Telling the user to go configure a domain would misdirect when the
		// domains they already have are the thing that could not be read.
		if readErr != nil {
			return readErr
		}
		return fmt.Errorf(
			"no hosts were configured - " +
				"ensure you've logged in via oauth2 and configured " +
				"at least one container registry domain for a host")
	}

	dir, err := dockercredhelper.ConfigDir()
	if err != nil {
		return err
	}

	registrations, err := dockercredhelper.Register(dir, configuredDomains...)
	if err != nil {
		return err
	}

	for _, registration := range registrations {
		iostreams.LogInfof("%s Configured Docker credential helper for %s\n", iostreams.Color().GreenCheck(), registration.Domain)
		if registration.ShadowedLogin {
			iostreams.LogErrorf("%s %s\n", iostreams.Color().WarnIcon(), registration.ShadowedLoginWarning())
		}
	}

	// readErr still fails the command even though every readable host was
	// configured above: a CI script gating on `configure-docker` (for
	// example, `glab auth configure-docker && docker pull ...`) needs a
	// non-zero exit to detect that a host's registries were left
	// unauthenticated, rather than only finding out later from an
	// unrelated-looking `docker pull` failure.
	return readErr
}
