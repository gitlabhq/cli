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

		sshTranslate := git.ParseSSHConfig().Translator()
		resolvedRemotes := glrepo.TranslateRemotes(
			gitRemotes,
			func(u *url.URL) *url.URL {
				switch u.Scheme {
				case "ssh":
					return sshTranslate(u)
				default:
					return u
				}
			},
			rr.defaultHostname,
			cfg,
		)

		knownHosts := map[string]bool{}
		sshHostMapping := map[string]string{} // Maps SSH hostnames to config keys
		knownHosts[glinstance.DefaultHostname] = true
		if authenticatedHosts, err := cfg.Hosts(); err == nil {
			for _, h := range authenticatedHosts {
				knownHosts[h] = true

				// Build SSH host mapping
				if sshHost, _ := cfg.Get(h, "ssh_host"); sshHost != "" {
					sshHostMapping[sshHost] = h
				}
			}
		}

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
			if !knownHosts[repoHost] {
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
