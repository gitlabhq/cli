package cmdutils

import (
	"cmp"
	"errors"
	"net/url"
	"sort"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
)

// ErrNoGitRemotes indicates the repository has no git remotes configured.
// Distinct from git.ErrNotAGitRepository: there is a repo, it just has
// nothing to resolve against.
var ErrNoGitRemotes = errors.New("no git remotes found")

type remoteResolver struct {
	readRemotes     func() (git.RemoteSet, error)
	getConfig       func() config.Config
	parseSSHConfig  func() git.SSHAliasMap
	defaultHostname string
}

func (rr *remoteResolver) Resolver(hostOverride string) func() (glrepo.Remotes, error) {
	var cachedRemotes glrepo.Remotes
	var remotesError error

	return func() (glrepo.Remotes, error) {
		if cachedRemotes != nil || remotesError != nil {
			return cachedRemotes, remotesError
		}

		gitRemotes, err := rr.readRemotes()
		if err != nil {
			remotesError = err
			return nil, err
		}
		if len(gitRemotes) == 0 {
			remotesError = ErrNoGitRemotes
			return nil, remotesError
		}

		cfg := rr.getConfig()

		// Collect the configured hosts before translating the remotes, so a remote
		// whose literal SSH host already matches a configured host can skip the
		// ssh_config translation below.
		knownHosts := map[string]struct{}{}
		configuredHosts := map[string]struct{}{} // Only the accounts present in the config file.
		sshHostMapping := map[string]string{}    // Maps SSH hostnames to config keys
		knownHosts[glinstance.DefaultHostname] = struct{}{}
		if authenticatedHosts, err := cfg.Hosts(); err == nil {
			for _, h := range authenticatedHosts {
				// Config host lookups are case-sensitive: cfg.Hosts() returns the raw
				// YAML keys and cfg.Get matches them literally, so keys go in verbatim.
				knownHosts[h] = struct{}{}
				configuredHosts[h] = struct{}{}

				// The ssh_host value is only ever compared against a lower-cased remote
				// host, so normalize it here to keep the two sides consistent.
				if sshHost, _ := cfg.Get(h, "ssh_host"); sshHost != "" {
					sshHostMapping[glinstance.NormalizeHostname(sshHost)] = h
				}
			}
		}

		sshTranslate := rr.parseSSHConfig().Translator()
		resolvedRemotes := glrepo.TranslateRemotes(
			gitRemotes,
			func(u *url.URL) *url.URL {
				if u.Scheme != "ssh" {
					return u
				}
				// A remote whose literal SSH host is already a configured account (a
				// config key or an entry's ssh_host) resolves to that account, exactly as
				// an HTTPS remote would; only unknown hosts fall back to ssh_config
				// translation. The default gitlab.com is deliberately excluded here: when
				// it is not a configured account, an alias pointing it elsewhere is a
				// redirect to another instance and must be followed, as git does.
				// See https://gitlab.com/gitlab-org/cli/-/issues/8394.
				host := glinstance.NormalizeHostname(u.Hostname())
				if _, configured := configuredHosts[host]; configured {
					return u
				}
				if _, mapped := sshHostMapping[host]; mapped {
					return u
				}
				return sshTranslate(u)
			},
			rr.defaultHostname,
			cfg,
		)

		// filter remotes to only those sharing a single, known hostname
		var hostname string
		cachedRemotes = glrepo.Remotes{}
		sort.Sort(resolvedRemotes)

		if hostOverride != "" {
			for _, r := range resolvedRemotes {
				repoHost := r.RepoHost()
				if !strings.EqualFold(repoHost, hostOverride) {
					// Check if this is an SSH host that maps to the override
					if configHost, found := sshHostMapping[repoHost]; found && strings.EqualFold(configHost, hostOverride) {
						r = &glrepo.Remote{
							Remote: r.Remote,
							Repo:   glrepo.NewWithHost(r.RepoOwner(), r.RepoName(), configHost),
						}
					} else {
						continue
					}
				}
				cachedRemotes = append(cachedRemotes, r)
			}

			if len(cachedRemotes) == 0 {
				remotesError = errors.New("none of the git remotes configured for this repository correspond to the GITLAB_HOST environment variable. Try adding a matching remote or unsetting the variable.\n\n" +
					"GITLAB_HOST is currently set to " + hostOverride + "\n\nConfigured remotes: " + resolvedRemotes.UniqueHosts())
				return nil, remotesError
			}

			return cachedRemotes, nil
		}

		for _, r := range resolvedRemotes {
			repoHost := r.RepoHost()

			// Check if this is a known host or SSH host that maps to a config entry
			if _, ok := knownHosts[repoHost]; !ok {
				configHost, found := sshHostMapping[repoHost]
				if !found {
					// Unknown host - skip this remote
					continue
				}
				// SSH host maps to a config entry — update both repoHost and r
				// so that RepoHost() returns the API hostname and filtering works correctly
				repoHost = configHost
				r = &glrepo.Remote{
					Remote: r.Remote,
					Repo:   glrepo.NewWithHost(r.RepoOwner(), r.RepoName(), configHost),
				}
			}

			if repoHost != hostname && hostname != "" {
				continue
			}

			hostname = cmp.Or(hostname, repoHost)
			cachedRemotes = append(cachedRemotes, r)
		}

		if len(cachedRemotes) == 0 {
			remotesError = errors.New("none of the git remotes configured for this repository point to a known GitLab host. Please use `glab auth login` to authenticate and configure a new host for glab.\n\n" +
				"Configured remotes: " + resolvedRemotes.UniqueHosts())
			return nil, remotesError
		}
		return cachedRemotes, nil
	}
}
