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
			defaultHostname: "gitlab.com",
		}

		resolver := rr.Resolver("")
		remotes, err := resolver()
		require.NoError(t, err)
		require.Len(t, remotes, 1)

		// BUG: The remote should have RepoHost() = "api.example.com" (the config key)
		// but it actually returns "git.example.com" (the SSH hostname)
		// This causes downstream code to fail when looking up config (subfolder, token, etc.)

		// This assertion should PASS after the fix:
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
			defaultHostname: "gitlab.com",
		}

		resolver := rr.Resolver("")
		remotes, err := resolver()
		require.NoError(t, err)

		// CRITICAL: Both remotes should be returned
		// MR #2924 bug: Only returns 1 remote (filters out upstream)
		// Our fix: Returns 2 remotes correctly
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
