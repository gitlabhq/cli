package login

import (
	"fmt"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dockercredhelper"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// loginDocker configures Docker to authenticate against registry using
// glab's Docker credential helper: it installs the docker-credential-glab
// shim, wires it into Docker's credHelpers for registry, and records
// registry in hostname's artifact_registry_domains config key so the
// credential helper (internal/commands/auth/docker.Helper.Get) can find it
// later.
func loginDocker(io *iostreams.IOStreams, cfg config.Config, hostname, registry string) error {
	if _, err := dockercredhelper.Install(); err != nil {
		return err
	}

	dir, err := dockercredhelper.ConfigDir()
	if err != nil {
		return err
	}

	registrations, err := dockercredhelper.Register(dir, registry)
	if err != nil {
		return err
	}

	// Read-modify-write, not compare-and-swap: two concurrent `glab
	// artifact-registry login` runs against the same $HOME still race when they
	// add different domains, and the last writer's update wins. Acceptable for
	// an interactively-invoked CLI.
	domains, _, err := cfg.GetWithSource(hostname, "artifact_registry_domains", false)
	if err != nil {
		return unrecordedRegistryError(registry, fmt.Errorf("reading artifact_registry_domains: %w", err))
	}

	// The Set and the Write are skipped when the domain is already recorded, not
	// just the append: a re-login has nothing to add, and writing anyway would
	// rewrite the file from this snapshot, dropping a domain a concurrent login
	// added after the read. That narrows the race above to logins that really
	// change something.
	if parsed := config.ParseDomains(domains); !slices.Contains(parsed, registry) {
		parsed = append(parsed, registry)

		if err := cfg.Set(hostname, "artifact_registry_domains", strings.Join(parsed, ",")); err != nil {
			return unrecordedRegistryError(registry, fmt.Errorf("setting artifact_registry_domains: %w", err))
		}
		if err := cfg.Write(); err != nil {
			return unrecordedRegistryError(registry, fmt.Errorf("writing config: %w", err))
		}
	}

	io.LogInfof("%s Configured Docker credential helper for %s.\n", io.Color().GreenCheck(), registry)
	for _, reg := range registrations {
		if reg.ShadowedLogin {
			io.LogErrorf("%s %s\n", io.Color().WarnIcon(), reg.ShadowedLoginWarning())
		}
	}

	warnIfAnotherHostResolvesRegistry(io, cfg, hostname, registry)
	warnIfContainerRegistryDomain(io, cfg, hostname, registry)

	return nil
}

// unrecordedRegistryError annotates any configuration failure between
// Register and the config write landing, by which point Docker's credHelpers
// already points at glab for registry.
// Docker now calls the credential helper, which resolves a registry through
// artifact_registry_domains and will not find this one, so `docker pull` fails
// until the value is recorded.
//
// Register deliberately runs first, so a registry another helper already owns
// is refused without touching the configuration file. That ordering makes this
// window the price, so name it in the error rather than reporting a bare write
// failure.
func unrecordedRegistryError(registry string, err error) error {
	return fmt.Errorf("%w; Docker is already pointed at glab for %s but the registry was not recorded, so docker pull fails against it until this command is rerun", err, registry)
}

// warnIfAnotherHostResolvesRegistry warns when a host other than hostname is
// the one the Docker credential helper will resolve registry through.
// artifact_registry_domains is per-host, but the helper takes the first host
// that lists the domain (internal/commands/auth/docker.Helper's
// findHostnameByDomain, over cfg.Hosts(), which puts gitlab.com first and then
// keeps configuration order), so registering a domain a second time under a
// later host writes an entry nothing will ever read: this login reports
// success while `docker pull` keeps authenticating against the earlier host.
//
// Called after the write, so the host being configured is already in the list
// and the loop below sees exactly the state the helper will see.
//
// A read failure is reported and skipped, as in warnIfContainerRegistryDomain.
// That can make an unreadable earlier host look like it does not list the
// domain, but the failure is on stderr either way.
func warnIfAnotherHostResolvesRegistry(io *iostreams.IOStreams, cfg config.Config, hostname, registry string) {
	hostnames, err := cfg.Hosts()
	if err != nil {
		io.LogErrorf("%s Could not check whether another host also lists %s: %v\n", io.Color().WarnIcon(), registry, err)
		return
	}

	for _, candidate := range hostnames {
		domains, _, err := cfg.GetWithSource(candidate, "artifact_registry_domains", false)
		if err != nil {
			io.LogErrorf("%s Could not read artifact_registry_domains for %s: %v\n", io.Color().WarnIcon(), candidate, err)
			continue
		}
		if !slices.Contains(config.ParseDomains(domains), registry) {
			continue
		}

		if candidate == hostname {
			return
		}

		io.LogErrorf("%s %s is also listed in artifact_registry_domains for %s. The Docker credential helper takes the first host that lists a domain, so docker pull authenticates against %s, not %s. To use %s, drop %s from the other host with `glab config set artifact_registry_domains <remaining domains> --host %s`.\n",
			io.Color().WarnIcon(), registry, candidate, candidate, hostname, hostname, registry, candidate)
		return
	}
}

// warnIfContainerRegistryDomain warns when registry is also listed in some
// host's container_registry_domains. Both keys feed the same credential
// helper, which prefers the artifact-registry token exchange and falls back to
// the container-registry credentials only when that exchange fails
// (internal/commands/auth/docker.Helper.Get).
//
// The warning says the fallback will not fire, because for this domain it
// cannot: run only reaches loginDocker once the exchange has succeeded, so
// Helper.Get takes the artifact-registry branch and returns. The dangerous
// case is a container registry listed here by mistake, which
// `glab auth login` puts in container_registry_domains for gitlab.com,
// registry.gitlab.com, and gitlab.com:443 by default: `docker pull` worked
// before this login and now hard-fails on an artifact-registry token the
// container registry rejects. So the message leads with undoing this login,
// and keeps dropping the container_registry_domains entry as the remedy for a
// domain the Artifact Registry really backs.
//
// A read failure here is reported, not returned: the login itself already
// succeeded, and failing it now would misreport work that is on disk.
func warnIfContainerRegistryDomain(io *iostreams.IOStreams, cfg config.Config, arHostname, registry string) {
	hostnames, err := cfg.Hosts()
	if err != nil {
		io.LogErrorf("%s Could not check whether %s is also a container registry domain: %v\n", io.Color().WarnIcon(), registry, err)
		return
	}

	for _, hostname := range hostnames {
		domains, _, err := cfg.GetWithSource(hostname, "container_registry_domains", false)
		if err != nil {
			io.LogErrorf("%s Could not read container_registry_domains for %s: %v\n", io.Color().WarnIcon(), hostname, err)
			continue
		}
		if slices.Contains(config.ParseDomains(domains), registry) {
			io.LogErrorf("%s %s is also listed in container_registry_domains for %s. The Docker credential helper falls back to those credentials only when the artifact registry exchange fails, and this login just verified the exchange succeeds, so the fallback will not fire: docker pull now authenticates with an artifact registry token. If %s is a container registry, it rejects that token; undo this login with `glab config set artifact_registry_domains <remaining domains> --host %s`. If the Artifact Registry backs %s, drop the container_registry_domains entry instead with `glab config set container_registry_domains <remaining domains> --host %s`.\n",
				io.Color().WarnIcon(), registry, hostname, registry, arHostname, registry, hostname)
		}
	}
}
