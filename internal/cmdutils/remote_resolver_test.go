//go:build !integration

package cmdutils

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
)

// noSSHAliases injects an empty ~/.ssh/config into the resolver, so tests that do not
// exercise SSH-alias translation stay independent of the developer's real config.
func noSSHAliases() git.SSHAliasMap { return nil }

func Test_remoteResolver(t *testing.T) {
	rr := &remoteResolver{
		readRemotes: func() (git.RemoteSet, error) {
			return git.RemoteSet{
				git.NewRemote("fork", "https://example.org/owner/fork.git"),
				git.NewRemote("origin", "https://gitlab.com/owner/repo.git"),
				git.NewRemote("upstream", "https://example.org/owner/repo.git"),
			}, nil
		},
		getConfig: func() config.Config {
			return config.NewFromString(heredoc.Doc(`
				hosts:
				  example.org:
				    oauth_token: OTOKEN
			`))
		},
		parseSSHConfig: noSSHAliases,
	}

	resolver := rr.Resolver("")
	remotes, err := resolver()
	require.NoError(t, err)
	require.Len(t, remotes, 2)

	assert.Equal(t, "upstream", remotes[0].Name)
	assert.Equal(t, "fork", remotes[1].Name)
}

func Test_remoteResolverOverride(t *testing.T) {
	rr := &remoteResolver{
		readRemotes: func() (git.RemoteSet, error) {
			return git.RemoteSet{
				git.NewRemote("fork", "https://example.org/ghe-owner/ghe-fork.git"),
				git.NewRemote("origin", "https://gitlab.com/owner/repo.git"),
				git.NewRemote("upstream", "https://example.org/ghe-owner/ghe-repo.git"),
			}, nil
		},
		getConfig: func() config.Config {
			return config.NewFromString(heredoc.Doc(`
				hosts:
				  example.org:
				    oauth_token: GHETOKEN
			`))
		},
		parseSSHConfig: noSSHAliases,
	}

	resolver := rr.Resolver("gitlab.com")
	remotes, err := resolver()
	require.NoError(t, err)
	require.Len(t, remotes, 1)

	assert.Equal(t, "origin", remotes[0].Name)
}

func Test_remoteResolverSSHHostMapping(t *testing.T) {
	rr := &remoteResolver{
		readRemotes: func() (git.RemoteSet, error) {
			return git.RemoteSet{
				git.NewRemote("origin", "ssh://git@ssh.gitlab.example.com/owner/repo.git"),
			}, nil
		},
		getConfig: func() config.Config {
			return config.NewFromString(heredoc.Doc(`
				hosts:
				  gitlab.example.com:
				    oauth_token: OTOKEN
				    ssh_host: ssh.gitlab.example.com
			`))
		},
		parseSSHConfig: noSSHAliases,
	}

	resolver := rr.Resolver("")
	remotes, err := resolver()
	require.NoError(t, err)
	require.Len(t, remotes, 1)

	assert.Equal(t, "origin", remotes[0].Name)
	// The remote's RepoHost should be rewritten to the API hostname
	assert.Equal(t, "gitlab.example.com", remotes[0].RepoHost())
	assert.Equal(t, "owner", remotes[0].RepoOwner())
	assert.Equal(t, "repo", remotes[0].RepoName())
}

func Test_remoteResolverSameHostSSHAlias(t *testing.T) {
	// A remote whose literal SSH host is already a configured host (a config key or an
	// entry's ssh_host) must resolve to that account, regardless of what ~/.ssh/config
	// would rewrite it to; genuine aliases for unknown hosts still translate. See #8394.
	//
	// The sshAliases map is what ParseSSHConfig would return for the corresponding
	// ~/.ssh/config, injected through the parseSSHConfig seam.
	tests := []struct {
		name       string
		sshAliases git.SSHAliasMap
		remoteURL  string
		config     string
		wantHost   string
	}{
		{
			name:       "second same-host account resolves to its own key (#8394)",
			sshAliases: git.SSHAliasMap{"gitlab.com-work": "gitlab.com"},
			remoteURL:  "git@gitlab.com-work:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  gitlab.com:
				    token: PRIMARY
				  gitlab.com-work:
				    api_host: gitlab.com
				    ssh_host: gitlab.com
				    token: WORK
			`),
			wantHost: "gitlab.com-work",
		},
		{
			name:       "primary account still resolves over the shared host (#8394)",
			sshAliases: git.SSHAliasMap{"gitlab.com-work": "gitlab.com"},
			remoteURL:  "git@gitlab.com:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  gitlab.com:
				    token: PRIMARY
				  gitlab.com-work:
				    api_host: gitlab.com
				    ssh_host: gitlab.com
				    token: WORK
			`),
			wantHost: "gitlab.com",
		},
		{
			name:       "genuine alias for an unknown host still translates",
			sshAliases: git.SSHAliasMap{"gl": "gitlab.example.com"},
			remoteURL:  "git@gl:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  gitlab.example.com:
				    token: OTOKEN
			`),
			wantHost: "gitlab.example.com",
		},
		{
			name:       "configured host aliased away with no ssh_host resolves to that host",
			sshAliases: git.SSHAliasMap{"gitlab.example.com": "internal.example.com"},
			remoteURL:  "git@gitlab.example.com:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  gitlab.example.com:
				    token: OTOKEN
			`),
			wantHost: "gitlab.example.com",
		},
		{
			name:       "gitlab.com aliased to a non-official SSH endpoint resolves to gitlab.com",
			sshAliases: git.SSHAliasMap{"gitlab.com": "gitlab-proxy.corp.example.com"},
			remoteURL:  "git@gitlab.com:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  gitlab.com:
				    token: OTOKEN
			`),
			wantHost: "gitlab.com",
		},
		{
			name:       "ssh_host value is matched before ssh_config translation",
			sshAliases: git.SSHAliasMap{"git.example.com": "git-internal.example.com"},
			remoteURL:  "git@git.example.com:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  api.example.com:
				    token: OTOKEN
				    ssh_host: git.example.com
			`),
			wantHost: "api.example.com",
		},
		{
			// A mixed-case ssh_host value must still map, exercising NormalizeHostname
			// on the sshHostMapping populate side. (Config keys, by contrast, are
			// matched case-sensitively, so they are intentionally not normalized.)
			name:       "mixed-case ssh_host value still maps to its config key",
			sshAliases: git.SSHAliasMap{},
			remoteURL:  "git@ssh.example.com:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  api.example.com:
				    token: OTOKEN
				    ssh_host: SSH.Example.com
			`),
			wantHost: "api.example.com",
		},
		{
			// gitlab.com is not a configured host here, so the alias is a redirect to
			// another instance, not an identity alias: follow it, as git does.
			name:       "gitlab.com aliased to a configured host follows the alias",
			sshAliases: git.SSHAliasMap{"gitlab.com": "gitlab.corp.com"},
			remoteURL:  "git@gitlab.com:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  gitlab.corp.com:
				    token: OTOKEN
			`),
			wantHost: "gitlab.corp.com",
		},
		{
			// Same redirect, reached through the target's ssh_host rather than its key.
			name:       "gitlab.com aliased to a configured host's ssh_host follows the alias",
			sshAliases: git.SSHAliasMap{"gitlab.com": "ssh.gitlab.corp.com"},
			remoteURL:  "git@gitlab.com:owner/repo.git",
			config: heredoc.Doc(`
				hosts:
				  gitlab.corp.com:
				    token: OTOKEN
				    ssh_host: ssh.gitlab.corp.com
			`),
			wantHost: "gitlab.corp.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sshURL, err := git.ParseURL(tt.remoteURL)
			require.NoError(t, err)

			rr := &remoteResolver{
				readRemotes: func() (git.RemoteSet, error) {
					return git.RemoteSet{
						&git.Remote{Name: "origin", FetchURL: sshURL, PushURL: sshURL},
					}, nil
				},
				getConfig: func() config.Config {
					return config.NewFromString(tt.config)
				},
				parseSSHConfig:  func() git.SSHAliasMap { return tt.sshAliases },
				defaultHostname: "gitlab.com",
			}

			resolver := rr.Resolver("")
			remotes, err := resolver()
			require.NoError(t, err)
			require.Len(t, remotes, 1)

			assert.Equal(t, "origin", remotes[0].Name)
			assert.Equal(t, tt.wantHost, remotes[0].RepoHost())
			assert.Equal(t, "owner", remotes[0].RepoOwner())
			assert.Equal(t, "repo", remotes[0].RepoName())
		})
	}
}

func Test_remoteResolverSSHHostMappingEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		remotes       git.RemoteSet
		config        string
		hostOverride  string
		expectedCount int
		expectedHost  string
		expectedName  string
		expectedError string
	}{
		{
			name: "SSH and HTTPS remotes coexist, same project",
			remotes: git.RemoteSet{
				git.NewRemote("origin", "ssh://git@ssh.gitlab.example.com/owner/repo.git"),
				git.NewRemote("https-origin", "https://gitlab.example.com/owner/repo.git"),
			},
			config: heredoc.Doc(`
				hosts:
				  gitlab.example.com:
				    oauth_token: OTOKEN
				    ssh_host: ssh.gitlab.example.com
			`),
			expectedCount: 2,
			expectedHost:  "gitlab.example.com",
			expectedName:  "origin",
		},
		{
			name: "SSH host matches but no ssh_host config — remote skipped",
			remotes: git.RemoteSet{
				git.NewRemote("origin", "ssh://git@ssh.gitlab.example.com/owner/repo.git"),
			},
			config: heredoc.Doc(`
				hosts:
				  gitlab.example.com:
				    oauth_token: OTOKEN
			`),
			expectedError: "none of the git remotes configured for this repository point to a known GitLab host",
		},
		{
			name: "Standard setup without ssh_host — no change in behavior",
			remotes: git.RemoteSet{
				git.NewRemote("origin", "https://gitlab.com/owner/repo.git"),
			},
			config: heredoc.Doc(`
				hosts:
				  gitlab.com:
				    oauth_token: OTOKEN
			`),
			expectedCount: 1,
			expectedHost:  "gitlab.com",
			expectedName:  "origin",
		},
		{
			name: "Self-managed with matching SSH and API hostname — no rewrite needed",
			remotes: git.RemoteSet{
				git.NewRemote("origin", "ssh://git@gitlab.corp.com/owner/repo.git"),
			},
			config: heredoc.Doc(`
				hosts:
				  gitlab.corp.com:
				    oauth_token: OTOKEN
			`),
			expectedCount: 1,
			expectedHost:  "gitlab.corp.com",
			expectedName:  "origin",
		},
		{
			name: "SSH host mapping with host override matching API host",
			remotes: git.RemoteSet{
				git.NewRemote("origin", "ssh://git@ssh.gitlab.example.com/owner/repo.git"),
			},
			config: heredoc.Doc(`
				hosts:
				  gitlab.example.com:
				    oauth_token: OTOKEN
				    ssh_host: ssh.gitlab.example.com
			`),
			hostOverride:  "gitlab.example.com",
			expectedCount: 1,
			expectedHost:  "gitlab.example.com",
			expectedName:  "origin",
		},
		{
			name: "SSH host mapping with host override matching SSH host — matches raw remote",
			remotes: git.RemoteSet{
				git.NewRemote("origin", "ssh://git@ssh.gitlab.example.com/owner/repo.git"),
			},
			config: heredoc.Doc(`
				hosts:
				  gitlab.example.com:
				    oauth_token: OTOKEN
				    ssh_host: ssh.gitlab.example.com
			`),
			hostOverride:  "ssh.gitlab.example.com",
			expectedCount: 1,
			expectedHost:  "ssh.gitlab.example.com",
			expectedName:  "origin",
		},
		{
			name: "Multiple hosts, SSH mapping only for one",
			remotes: git.RemoteSet{
				git.NewRemote("corp", "ssh://git@ssh.corp.example.com/team/project.git"),
				git.NewRemote("community", "https://gitlab.com/team/project.git"),
			},
			config: heredoc.Doc(`
				hosts:
				  corp.example.com:
				    oauth_token: CORPTOKEN
				    ssh_host: ssh.corp.example.com
				  gitlab.com:
				    oauth_token: COMTOKEN
			`),
			// First match wins (sorted by remote name priority)
			expectedCount: 1,
			expectedHost:  "corp.example.com",
			expectedName:  "corp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := &remoteResolver{
				readRemotes: func() (git.RemoteSet, error) {
					return tt.remotes, nil
				},
				getConfig: func() config.Config {
					return config.NewFromString(tt.config)
				},
				parseSSHConfig: noSSHAliases,
			}

			resolver := rr.Resolver(tt.hostOverride)
			remotes, err := resolver()

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			require.Len(t, remotes, tt.expectedCount)
			assert.Equal(t, tt.expectedName, remotes[0].Name)
			assert.Equal(t, tt.expectedHost, remotes[0].RepoHost())
		})
	}
}

func Test_remoteResolverErrors(t *testing.T) {
	testRemotes := git.RemoteSet{
		git.NewRemote("origin", "https://example3.org/owner/fork.git"),
		git.NewRemote("fork", "https://example.org/owner/fork.git"),
		git.NewRemote("upstream", "https://example.org/owner/repo.git"),
		git.NewRemote("foo", "https://example2.org/owner/repo.git"),
	}

	tests := []struct {
		name            string
		remotes         git.RemoteSet
		hostOverride    string
		expectedError   string
		expectedErrorIs error
	}{
		{
			name:            "No remotes",
			remotes:         git.RemoteSet{},
			expectedError:   "no git remotes found",
			expectedErrorIs: ErrNoGitRemotes,
		},
		{
			name:         "No match with host override",
			remotes:      testRemotes,
			hostOverride: "nomatch.org",
			expectedError: "none of the git remotes configured for this repository correspond to the GITLAB_HOST environment variable. " +
				"Try adding a matching remote or unsetting the variable.\n\n" +
				"GITLAB_HOST is currently set to nomatch.org\n\n" +
				"Configured remotes: example.org, example3.org, example2.org",
		},
		{
			name:    "No match",
			remotes: testRemotes,
			expectedError: "none of the git remotes configured for this repository point to a known GitLab host. " +
				"Please use `glab auth login` to authenticate and configure a new host for glab.\n\n" +
				"Configured remotes: example.org, example3.org, example2.org",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rr := &remoteResolver{
				readRemotes: func() (git.RemoteSet, error) {
					return test.remotes, nil
				},
				getConfig: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
				hosts:
				  my-gitlab.org:
				    oauth_token: OTOKEN
			`))
				},
				parseSSHConfig: noSSHAliases,
			}

			resolver := rr.Resolver(test.hostOverride)
			_, err := resolver()
			require.Error(t, err)
			assert.Equal(t, test.expectedError, err.Error())
			if test.expectedErrorIs != nil {
				require.ErrorIs(t, err, test.expectedErrorIs)
			}
		})
	}
}

func Test_remoteResolverSplitHostWithSubfolder(t *testing.T) {
	// This test reproduces Issue #8197: split-host + subfolder bug
	// where SSH host mapping happens but r.Repo is never updated with the mapped hostname
	t.Run("SSH remote with split-host config and subfolder", func(t *testing.T) {
		// Parse SSH URL using git.ParseURL to properly normalize SCP-style format
		sshURL, err := git.ParseURL("git@git.example.com:owner/repo.git")
		require.NoError(t, err)

		rr := &remoteResolver{
			readRemotes: func() (git.RemoteSet, error) {
				// SSH remote URL using git.example.com
				return git.RemoteSet{
					&git.Remote{
						Name:     "origin",
						FetchURL: sshURL,
						PushURL:  sshURL,
					},
				}, nil
			},
			getConfig: func() config.Config {
				// Config keyed under api.example.com with ssh_host pointing to git.example.com
				return config.NewFromString(heredoc.Doc(`
					hosts:
					  api.example.com:
					    token: TEST_TOKEN
					    ssh_host: git.example.com
					    subfolder: gitlab
				`))
			},
			parseSSHConfig:  noSSHAliases,
			defaultHostname: "gitlab.com",
		}

		resolver := rr.Resolver("")
		remotes, err := resolver()
		require.NoError(t, err)
		require.Len(t, remotes, 1)

		// The remote should have RepoHost() = "api.example.com" (the config key),
		// not "git.example.com" (the SSH hostname), so downstream config lookups
		// (subfolder, token, etc.) resolve correctly.
		assert.Equal(t, "api.example.com", remotes[0].RepoHost(),
			"Remote.Repo should be updated to use the config key hostname, not the SSH hostname")

		// Verify the owner/repo are correct
		assert.Equal(t, "owner", remotes[0].RepoOwner())
		assert.Equal(t, "repo", remotes[0].RepoName())
	})

	t.Run("SSH + HTTPS remotes with split-host: filtering bug in MR #2924", func(t *testing.T) {
		// This test exposes the filtering bug in MR #2924:
		// When both SSH (split-host) and HTTPS remotes exist,
		// MR #2924 incorrectly filters out the HTTPS remote

		sshURL, err := git.ParseURL("git@git.example.com:owner/repo.git")
		require.NoError(t, err)

		httpsURL, err := git.ParseURL("https://api.example.com/owner/upstream.git")
		require.NoError(t, err)

		rr := &remoteResolver{
			readRemotes: func() (git.RemoteSet, error) {
				return git.RemoteSet{
					&git.Remote{Name: "origin", FetchURL: sshURL, PushURL: sshURL},
					&git.Remote{Name: "upstream", FetchURL: httpsURL, PushURL: httpsURL},
				}, nil
			},
			getConfig: func() config.Config {
				return config.NewFromString(heredoc.Doc(`
					hosts:
					  api.example.com:
					    token: TEST_TOKEN
					    ssh_host: git.example.com
					    subfolder: gitlab
				`))
			},
			parseSSHConfig:  noSSHAliases,
			defaultHostname: "gitlab.com",
		}

		resolver := rr.Resolver("")
		remotes, err := resolver()
		require.NoError(t, err)

		// Both remotes should be returned (MR #2924 bug returned only 1).
		assert.Len(t, remotes, 2, "Both SSH and HTTPS remotes should be included")

		// Verify both remotes use the correct API hostname
		assert.Equal(t, "api.example.com", remotes[0].RepoHost())
		assert.Equal(t, "api.example.com", remotes[1].RepoHost())

		// Verify both remote names are present (order may vary due to sorting)
		names := []string{remotes[0].Name, remotes[1].Name}
		assert.Contains(t, names, "origin")
		assert.Contains(t, names, "upstream")
	})
}

// remoteResolver is the single path from raw git remotes to resolved repos that
// every command inherits through the factory, so exercising the port/subfolder
// matrix here covers all of them at once. Command-level tests cannot: they stub
// Factory.Remotes with repos that are already resolved, which is why a
// resolution bug could reach a release with the command suites green.
//
// Each case asserts the resolved project path and that RepoHost still addresses
// the configured host entry. RepoHost is what the API client keys its own
// lookups on, so a remote that resolves to a host with no entry sends
// unauthenticated requests over the default protocol.
func Test_remoteResolver_PortAndSubfolderMatrix(t *testing.T) {
	cases := []struct {
		name      string
		cfg       string
		remoteURL string
		wantOwner string
		wantName  string
		wantHost  string
	}{
		{
			name: "custom port with subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TEST_TOKEN
				    api_protocol: http
				    subfolder: gitlab
			`),
			remoteURL: "http://example.com:8080/gitlab/owner/repo.git",
			wantOwner: "owner",
			wantName:  "repo",
			wantHost:  "example.com:8080",
		},
		{
			name: "custom port without subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TEST_TOKEN
				    api_protocol: http
			`),
			remoteURL: "http://example.com:8080/owner/repo.git",
			wantOwner: "owner",
			wantName:  "repo",
			wantHost:  "example.com:8080",
		},
		{
			name: "subfolder without custom port",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TEST_TOKEN
				    subfolder: gitlab
			`),
			remoteURL: "https://example.com/gitlab/owner/repo.git",
			wantOwner: "owner",
			wantName:  "repo",
			wantHost:  "example.com",
		},
		{
			name: "custom port with nested subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TEST_TOKEN
				    api_protocol: http
				    subfolder: apps/gitlab
			`),
			remoteURL: "http://example.com:8080/apps/gitlab/owner/repo.git",
			wantOwner: "owner",
			wantName:  "repo",
			wantHost:  "example.com:8080",
		},
		{
			name: "custom port with subfolder from legacy api_host",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TEST_TOKEN
				    api_protocol: http
				    api_host: example.com:8080/gitlab
			`),
			remoteURL: "http://example.com:8080/gitlab/owner/repo.git",
			wantOwner: "owner",
			wantName:  "repo",
			wantHost:  "example.com:8080",
		},
		{
			name: "custom port with subfolder and a subgroup",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TEST_TOKEN
				    api_protocol: http
				    subfolder: gitlab
			`),
			remoteURL: "http://example.com:8080/gitlab/group/subgroup/repo.git",
			wantOwner: "group/subgroup",
			wantName:  "repo",
			wantHost:  "example.com:8080",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remoteURL, err := git.ParseURL(tc.remoteURL)
			require.NoError(t, err)

			cfg := config.NewFromString(tc.cfg)
			rr := &remoteResolver{
				readRemotes: func() (git.RemoteSet, error) {
					return git.RemoteSet{
						&git.Remote{Name: "origin", FetchURL: remoteURL, PushURL: remoteURL},
					}, nil
				},
				getConfig:       func() config.Config { return cfg },
				parseSSHConfig:  noSSHAliases,
				defaultHostname: "gitlab.com",
			}

			resolver := rr.Resolver("")
			remotes, err := resolver()
			require.NoError(t, err)
			require.Len(t, remotes, 1)

			assert.Equal(t, tc.wantOwner, remotes[0].RepoOwner())
			assert.Equal(t, tc.wantName, remotes[0].RepoName())
			assert.Equal(t, tc.wantHost, remotes[0].RepoHost())

			token, _ := cfg.Get(remotes[0].RepoHost(), "token")
			assert.Equal(t, "TEST_TOKEN", token,
				"RepoHost must address the configured host entry, or the API client authenticates as nobody")
		})
	}
}

// An SSH remote on a subfolder instance resolves to the ported host entry that
// names it through ssh_host, so downstream config lookups address the same
// entry an HTTPS remote would.
//
// The subfolder is deliberately absent from the SSH path. A relative URL root
// is an HTTP concept: GitLab reports ssh_url_to_repo as git@host:group/repo.git
// whatever prefix the web UI is served under, which is why the `subfolder` key
// is documented as applying to HTTP/HTTPS only. It stays in the fixture because
// a subfolder instance really does hand out SSH remotes like this, and the
// assertion pins that it is not mistakenly stripped from — or prepended to — a
// path that never carried it.
func Test_remoteResolver_SSHRemoteOnPortedSubfolderHost(t *testing.T) {
	t.Run("ssh_host mapping to a ported host entry", func(t *testing.T) {
		sshURL, err := git.ParseURL("git@git.example.com:owner/repo.git")
		require.NoError(t, err)

		cfg := config.NewFromString(heredoc.Doc(`
			hosts:
			  example.com:8080:
			    token: TEST_TOKEN
			    api_protocol: http
			    ssh_host: git.example.com
			    subfolder: gitlab
		`))

		rr := &remoteResolver{
			readRemotes: func() (git.RemoteSet, error) {
				return git.RemoteSet{
					&git.Remote{Name: "origin", FetchURL: sshURL, PushURL: sshURL},
				}, nil
			},
			getConfig:       func() config.Config { return cfg },
			parseSSHConfig:  noSSHAliases,
			defaultHostname: "gitlab.com",
		}

		resolver := rr.Resolver("")
		remotes, err := resolver()
		require.NoError(t, err)
		require.Len(t, remotes, 1)

		assert.Equal(t, "owner/repo", remotes[0].FullName(),
			"an SSH path carries no subfolder segment, so none is stripped and none is added")
		assert.Equal(t, "example.com:8080", remotes[0].RepoHost(),
			"the SSH remote should resolve to the config key, port included")

		token, _ := cfg.Get(remotes[0].RepoHost(), "token")
		assert.Equal(t, "TEST_TOKEN", token)
	})
}
