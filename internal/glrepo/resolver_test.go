//go:build !integration

package glrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	tea "charm.land/bubbletea/v2"
	"git.sr.ht/~timofurrer/ugh"
	"github.com/hashicorp/go-multierror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	testConsoleWidth  = 120
	testConsoleHeight = 40
)

func testIOStreamsWithConsole(t *testing.T, c *ugh.Console) (*iostreams.IOStreams, func()) {
	t.Helper()

	appInR, appInW := io.Pipe()
	appOutR, appOutW := io.Pipe()

	ctx, cancel := context.WithCancel(t.Context())
	wait := c.Start(ctx, appOutR, appInW)

	ios := iostreams.New(
		iostreams.WithStdin(appInR, true),
		iostreams.WithStdout(appOutW, true),
		iostreams.WithStderr(io.Discard, true),
		iostreams.WithProgramOptions(tea.WithWindowSize(testConsoleWidth, testConsoleHeight)),
	)

	cleanup := func() {
		appInW.Close()
		appOutW.Close()
		appOutR.Close()
		cancel()
		wait()
	}

	return ios, cleanup
}

func Test_RemoteForRepo(t *testing.T) {
	r := &ResolvedRemotes{
		remotes: Remotes{
			&Remote{
				Remote: &git.Remote{
					Name: "upstream",
				},
				Repo: NewWithHost("profclems", "glab", "gitlab.com"),
			},
			&Remote{
				Remote: &git.Remote{
					Name: "origin",
				},
				Repo: NewWithHost("maxice8", "glab", "gitlab.com"),
			},
		},
	}
	testCases := []struct {
		name    string
		input   Interface
		output  *Remote // Expected remote if there is a match
		wantErr string  // Expected error
	}{
		{
			name:  "match upstream",
			input: NewWithHost("profclems", "glab", "gitlab.com"),
			output: &Remote{
				Remote: &git.Remote{
					Name: "upstream",
				},
				Repo: NewWithHost("profclems", "glab", "gitlab.com"),
			},
		},
		{
			name:  "match origin",
			input: NewWithHost("maxice8", "glab", "gitlab.com"),
			output: &Remote{
				Remote: &git.Remote{
					Name: "origin",
				},
				Repo: NewWithHost("maxice8", "glab", "gitlab.com"),
			},
		},
		{
			name:    "no match via Hostname",
			input:   NewWithHost("profclems", "glab", "gitlab.extradomain.com"),
			wantErr: "not found",
		},
		{
			name:    "no match via Username",
			input:   NewWithHost("noexist", "glab", "gitlab.com"),
			wantErr: "not found",
		},
		{
			name:    "no match via Name",
			input:   NewWithHost("profclems", "maxice8", "gitlab.com"),
			wantErr: "not found",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got, err := r.RemoteForRepo(tC.input)
			if tC.wantErr == "" && err != nil {
				t.Errorf("RemoteForRepo() unexpected error = %s", err)
			}
			if tC.wantErr != "" {
				if tC.wantErr != err.Error() {
					t.Errorf("RemoteForRepo() expected error = %s, got = %s", tC.wantErr, err)
				}
			} else {
				// Make sure both return all the exact same thing
				assert.Equal(t, tC.output.Name, got.Name)
				assert.Equal(t, tC.output.Remote.Name, got.Remote.Name)
				assert.Equal(t, tC.output.Repo.FullName(), got.Repo.FullName())
				assert.Equal(t, tC.output.Repo.RepoHost(), got.Repo.RepoHost())
			}
		})
	}
}

func Test_ResolveRemotesToRepos(t *testing.T) {
	rem := &ResolvedRemotes{
		remotes: Remotes{
			&Remote{
				Remote: &git.Remote{
					Name: "origin",
				},
				Repo: NewWithHost("profclems", "glab", "gitlab.com"),
			},
		},
		apiClient: &gitlab.Client{},
	}

	// Test the normal and most expected usage
	t.Run("simple", func(t *testing.T) {
		r, err := ResolveRemotesToRepos(rem.remotes, rem.apiClient, glinstance.DefaultHostname, config.NewBlankConfig())
		require.NoError(t, err)

		assert.Equal(t, rem.apiClient, r.apiClient)

		assert.Len(t, r.remotes, 1)

		for i := range r.remotes {
			assert.Equal(t, r.remotes[i].Name, rem.remotes[i].Name)
			assert.Equal(t, r.remotes[i].Repo.FullName(), rem.remotes[i].Repo.FullName())
			assert.Equal(t, r.remotes[i].Repo.RepoHost(), rem.remotes[i].Repo.RepoHost())
		}
	})
}

func Test_resolveNetwork(t *testing.T) {
	rem := &ResolvedRemotes{
		remotes: Remotes{
			&Remote{
				Remote: &git.Remote{
					Name: "origin",
				},
				Repo: NewWithHost("profclems", "glab", "gitlab.com"),
			},
		},
		apiClient: &gitlab.Client{},
	}

	// Override api.GetProject to not use the network
	mockAPIGetProject := func(_ *gitlab.Client, ProjectID any) (*gitlab.Project, error) {
		proj := &gitlab.Project{
			PathWithNamespace: fmt.Sprint(ProjectID),
		}
		return proj, nil
	}

	t.Run("simple", func(t *testing.T) {
		// Make our own copy of rem we can modify
		rem := *rem

		api.GetProject = mockAPIGetProject

		err := resolveNetwork(&rem)

		require.NoError(t, err)
		assert.Len(t, rem.network, len(rem.remotes))
		for i := range rem.network {
			assert.Equal(t, rem.remotes[i].Repo.FullName(), rem.network[i].PathWithNamespace)
		}
	})

	t.Run("API call failed", func(t *testing.T) {
		// Make our own copy of rem we can modify
		rem := *rem

		// Override api.GetProject so it doesn't mess with other tests
		originalGetProject := api.GetProject
		defer func() {
			api.GetProject = originalGetProject
		}()
		api.GetProject = func(_ *gitlab.Client, ProjectID any) (*gitlab.Project, error) {
			return nil, assert.AnError
		}

		err := resolveNetwork(&rem)

		require.Error(t, err)
		require.ErrorIs(t, err, assert.AnError)
		assert.Empty(t, rem.network)
	})

	t.Run("MaxRemotesForLookup limit", func(t *testing.T) {
		// Make our own copy of rem we can modify
		rem := *rem

		api.GetProject = mockAPIGetProject

		for i := range maxRemotesForLookup {
			rem.remotes = append(rem.remotes, rem.remotes[i])
		}
		// Make sure we have at least one more remote than the limit set from maxRemotesForLookup
		assert.Len(t, rem.remotes, maxRemotesForLookup+1)

		err := resolveNetwork(&rem)

		require.NoError(t, err)
		assert.Len(t, rem.network, maxRemotesForLookup)
		for i := range rem.network {
			assert.Equal(t, rem.remotes[i].Repo.FullName(), rem.network[i].PathWithNamespace)
		}
	})
}

func Test_BaseRepo(t *testing.T) {
	// Make it a function that must be called by each test so none of them overlap
	rem := func() ResolvedRemotes {
		rem := &ResolvedRemotes{
			remotes: Remotes{
				&Remote{
					Remote: &git.Remote{
						Name: "upstream",
					},
					Repo: NewWithHost("profclems", "glab", "gitlab.com"),
				},
			},
			apiClient: &gitlab.Client{},
			network: []gitlab.Project{
				{
					ID:                1,
					PathWithNamespace: "profclems/glab",
					HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				},
			},
		}
		return *rem
	}

	mockGitlabProject := func(i any) gitlab.Project {
		p := &gitlab.Project{
			PathWithNamespace: fmt.Sprint(i),
			HTTPURLToRepo:     fmt.Sprintf("https://gitlab.com/%s", i),
		}
		return *p
	}

	// Override git.SetRemoteResolution so it doesn't mess with the user configs
	originalSetRemoteResolution := git.SetRemoteResolution
	defer func() {
		git.SetRemoteResolution = originalSetRemoteResolution
	}()
	git.SetRemoteResolution = func(_, _ string) error {
		return nil
	}

	// Override api.GetProject so it doesn't mess with other tests
	originalGetProject := api.GetProject
	defer func() {
		api.GetProject = originalGetProject
	}()
	api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
		p := mockGitlabProject(projectID)
		return &p, nil
	}

	t.Run("Resolved->base", func(t *testing.T) {
		localRem := rem()

		// Set a base resolution
		localRem.remotes[0].Resolved = "base"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].RepoHost(), got.RepoHost())
	})

	t.Run("Resolved->base:", func(t *testing.T) {
		localRem := rem()

		expectedResolution := NewWithHost("maxice8", "glab", "gitlab.com")

		// Set a base resolution
		localRem.remotes[0].Resolved = "base: gitlab.com/maxice8/glab"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, expectedResolution.FullName(), got.FullName())
		assert.Equal(t, expectedResolution.RepoHost(), got.RepoHost())
	})

	t.Run("Resolved->base: (invalid)", func(t *testing.T) {
		localRem := rem()

		// Set a base resolution
		localRem.remotes[0].Resolved = "base:NotAnActualValidValue"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		assert.Nil(t, got)
		assert.EqualError(t, err, `expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got "NotAnActualValidValue"`)
	})

	t.Run("Resolved->backwards-compatibility", func(t *testing.T) {
		localRem := rem()

		expectedResolution := NewWithHost("maxice8", "glab", "gitlab.com")

		// Set a base resolution
		localRem.remotes[0].Resolved = "gitlab.com/maxice8/glab"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, expectedResolution.FullName(), got.FullName())
		assert.Equal(t, expectedResolution.RepoHost(), got.RepoHost())
	})

	t.Run("Resolved->backwards-compatibility: (invalid)", func(t *testing.T) {
		localRem := rem()

		// Set a base resolution
		localRem.remotes[0].Resolved = "NotAnActualValidValue"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		assert.Nil(t, got)
		assert.EqualError(t, err, `expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got "NotAnActualValidValue"`)
	})

	t.Run("ResolvedBase->base (new field)", func(t *testing.T) {
		localRem := rem()

		// Set the new ResolvedBase field
		localRem.remotes[0].ResolvedBase = "base"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].RepoHost(), got.RepoHost())
	})

	t.Run("ResolvedBase->base: (new field with prefix)", func(t *testing.T) {
		localRem := rem()

		expectedResolution := NewWithHost("example", "glab", "gitlab.com")

		// Set the new ResolvedBase field with prefix
		localRem.remotes[0].ResolvedBase = "base: gitlab.com/example/glab"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, expectedResolution.FullName(), got.FullName())
		assert.Equal(t, expectedResolution.RepoHost(), got.RepoHost())
	})

	t.Run("ResolvedBase takes precedence over Resolved", func(t *testing.T) {
		localRem := rem()

		expectedResolution := NewWithHost("example", "glab", "gitlab.com")

		// Set both fields - new field should win
		localRem.remotes[0].ResolvedBase = "base: gitlab.com/example/glab"
		localRem.remotes[0].Resolved = "base: gitlab.com/different/repo"

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		// Should use ResolvedBase, not Resolved
		assert.Equal(t, expectedResolution.FullName(), got.FullName())
		assert.Equal(t, expectedResolution.RepoHost(), got.RepoHost())
	})

	t.Run("Prompt==false", func(t *testing.T) {
		localRem := rem()

		got, err := localRem.BaseRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network 1 repo", func(t *testing.T) {
		localRem := rem()

		// Prompt must be true otherwise we won't reach the code we want to test
		got, err := localRem.BaseRepo(t.Context(), iostreams.New(iostreams.WithStdout(nil, true), iostreams.WithStderr(nil, true)))
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network, no remotes", func(t *testing.T) {
		localRem := rem()

		// Wipe out all remotes
		localRem.remotes = Remotes{}
		localRem.network = nil

		_, err := localRem.BaseRepo(t.Context(), iostreams.New(iostreams.WithStdout(nil, true), iostreams.WithStderr(nil, true)))
		assert.EqualError(t, err, "no GitLab Projects found from remotes")
	})

	t.Run("Consult the network, multiple projects, pick origin", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
		}

		localRem.remotes = append(localRem.remotes, originRemote)
		localRem.network = append(localRem.network, originNetwork)

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the base repository")).
			Do(ugh.SelectIndex(1))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)

		got, err := localRem.BaseRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, originRemote.Repo.FullName(), got.FullName())
		assert.Equal(t, originRemote.Repo.RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network, multiple projects, pick upstream", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
		}

		localRem.remotes = append(localRem.remotes, originRemote)
		localRem.network = append(localRem.network, originNetwork)

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the base repository")).
			Do(ugh.SelectIndex(0))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)

		got, err := localRem.BaseRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].Repo.FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].Repo.RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network, one forked project, get fork", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
			ForkedFromProject: &gitlab.ForkParent{
				ID:                1,
				HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				PathWithNamespace: "profclems/glab",
			},
		}

		localRem.remotes = Remotes{originRemote}
		localRem.network = []gitlab.Project{originNetwork}

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the base repository")).
			Do(ugh.SelectIndex(1))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)

		got, err := localRem.BaseRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, originRemote.Repo.FullName(), got.FullName())
		assert.Equal(t, originRemote.Repo.RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network, one forked project, get upstream", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
			ForkedFromProject: &gitlab.ForkParent{
				ID:                1,
				HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				PathWithNamespace: "profclems/glab",
			},
		}

		localRem.remotes = Remotes{originRemote}
		localRem.network = []gitlab.Project{originNetwork}

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the base repository")).
			Do(ugh.SelectIndex(0))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)

		got, err := localRem.BaseRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, "profclems/glab", got.FullName())
		assert.Equal(t, "gitlab.com", got.RepoHost())
	})

	t.Run("Consult the network, all calls fail", func(t *testing.T) {
		// Override api.GetProject so it doesn't mess with other tests
		originalGetProject := api.GetProject
		defer func() {
			api.GetProject = originalGetProject
		}()
		api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
			return nil, assert.AnError
		}
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		localRem.remotes = append(localRem.remotes, originRemote)
		localRem.network = nil

		// Prompt must be true otherwise we won't reach the code we want to test
		_, err := localRem.BaseRepo(t.Context(), iostreams.New(iostreams.WithStdout(nil, true), iostreams.WithStderr(nil, true)))
		multierr := func() *multierror.Error {
			target := &multierror.Error{}
			_ = errors.As(err, &target)
			return target
		}()
		assert.Len(t, multierr.Errors, 2)
		assert.ErrorIs(t, err, assert.AnError, "Unexpected error type")
	})

	t.Run("Consult the network, some, but not all, calls fail", func(t *testing.T) {
		// Override api.GetProject so it doesn't mess with other tests
		originalGetProject := api.GetProject
		defer func() {
			api.GetProject = originalGetProject
		}()
		api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
			if projectID == "profclems/glab" {
				return &gitlab.Project{
					ID:                1,
					PathWithNamespace: "profclems/glab",
					HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				}, nil
			}
			return nil, assert.AnError
		}
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		localRem.remotes = append(localRem.remotes, originRemote)
		localRem.network = nil

		// Prompt must be true otherwise we won't reach the code we want to test
		_, err := localRem.BaseRepo(t.Context(), iostreams.New(iostreams.WithStdout(nil, true), iostreams.WithStderr(nil, true)))
		assert.NoError(t, err)
	})

	t.Run("Host mismatch: API host differs from git remote host", func(t *testing.T) {
		// This test verifies our fix for the host mismatch bug
		// where the API returns a different host than the git remote
		// Git remote uses git.example.com, but API returns api.example.com
		localRem := &ResolvedRemotes{
			remotes: Remotes{
				&Remote{
					Remote: &git.Remote{
						Name: "origin",
					},
					Repo: NewWithHost("owner", "repo", "git.example.com"), // Git remote uses git host
				},
			},
			apiClient: &gitlab.Client{},
			network: []gitlab.Project{
				{
					ID:                1,
					PathWithNamespace: "owner/repo",
					HTTPURLToRepo:     "https://api.example.com/owner/repo.git", // API returns api.example.com, different from git remote's git.example.com
				},
			},
		}

		// Override git.SetRemoteResolution so it doesn't mess with the user configs
		originalSetRemoteResolution := git.SetRemoteResolution
		defer func() {
			git.SetRemoteResolution = originalSetRemoteResolution
		}()
		git.SetRemoteResolution = func(_, _ string) error {
			return nil
		}

		// Override api.GetProject so it doesn't mess with other tests
		originalGetProject := api.GetProject
		defer func() {
			api.GetProject = originalGetProject
		}()
		api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
			p := &gitlab.Project{
				PathWithNamespace: fmt.Sprint(projectID),
				HTTPURLToRepo:     "https://api.example.com/owner/repo.git", // API returns api.example.com, different from git remote's git.example.com
			}
			return p, nil
		}

		ios := iostreams.New(iostreams.WithStdout(io.Discard, true), iostreams.WithStderr(io.Discard, true))

		got, err := localRem.BaseRepo(t.Context(), ios)
		require.NoError(t, err)

		// The fix should ensure we use the git remote host, not the API host
		assert.Equal(t, "owner/repo", got.FullName())
		assert.Equal(t, "git.example.com", got.RepoHost()) // Should use git remote host, not API host
	})

	t.Run("Host mismatch with subfolder: API host differs from git remote host and includes subfolder", func(t *testing.T) {
		// This test verifies the split-host + subfolder bug (Issue #8197)
		// where the API returns a different host than the git remote AND includes a subfolder
		// Git remote uses git.example.com, but API returns api.example.com/gitlab/owner/repo.git

		// Mock config with subfolder - using CORRECT config pattern (API hostname as key)
		cfg := config.NewFromString(`---
hosts:
  api.example.com:
    token: TEST_TOKEN
    ssh_host: git.example.com
    subfolder: gitlab
`)

		localRem := &ResolvedRemotes{
			remotes: Remotes{
				&Remote{
					Remote: &git.Remote{
						Name: "origin",
					},
					Repo: NewWithHost("owner", "repo", "git.example.com"), // Git remote uses git host
				},
			},
			apiClient:       &gitlab.Client{},
			defaultHostname: "gitlab.com",
			cfg:             cfg,
			network: []gitlab.Project{
				{
					ID:                1,
					PathWithNamespace: "owner/repo",                                    // API provides correct path WITHOUT subfolder
					HTTPURLToRepo:     "https://api.example.com/gitlab/owner/repo.git", // URL includes subfolder
				},
			},
		}

		// Override git.SetRemoteResolution so it doesn't mess with the user configs
		originalSetRemoteResolution := git.SetRemoteResolution
		defer func() {
			git.SetRemoteResolution = originalSetRemoteResolution
		}()
		git.SetRemoteResolution = func(_, _ string) error {
			return nil
		}

		// Override api.GetProject so it doesn't mess with other tests
		originalGetProject := api.GetProject
		defer func() {
			api.GetProject = originalGetProject
		}()
		api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
			p := &gitlab.Project{
				PathWithNamespace: "owner/repo", // API returns correct path
				HTTPURLToRepo:     "https://api.example.com/gitlab/owner/repo.git",
			}
			return p, nil
		}

		ios := iostreams.New(iostreams.WithStdout(nil, true), iostreams.WithStderr(nil, true))

		got, err := localRem.BaseRepo(t.Context(), ios)
		require.NoError(t, err)

		// The fix should ensure we get the correct path WITHOUT the subfolder prefix
		assert.Equal(t, "owner/repo", got.FullName())
		assert.Equal(t, "git.example.com", got.RepoHost())
	})
}

func Test_HeadRepo(t *testing.T) {
	// Make it a function that must be called by each test so none of them overlap
	rem := func() ResolvedRemotes {
		rem := &ResolvedRemotes{
			remotes: Remotes{
				&Remote{
					Remote: &git.Remote{
						Name: "upstream",
					},
					Repo: NewWithHost("profclems", "glab", "gitlab.com"),
				},
			},
			apiClient: &gitlab.Client{},
			network: []gitlab.Project{
				{
					ID:                1,
					PathWithNamespace: "profclems/glab",
					HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				},
			},
		}
		return *rem
	}

	mockGitlabProject := func(i any) gitlab.Project {
		p := &gitlab.Project{
			PathWithNamespace: fmt.Sprint(i),
			HTTPURLToRepo:     fmt.Sprintf("https://gitlab.com/%s", i),
		}
		return *p
	}

	// Override git.SetRemoteResolution so it doesn't mess with the user configs
	originalSetRemoteResolution := git.SetRemoteResolution
	defer func() {
		git.SetRemoteResolution = originalSetRemoteResolution
	}()
	git.SetRemoteResolution = func(_, _ string) error {
		return nil
	}

	// Override api.GetProject so it doesn't mess with other tests
	originalGetProject := api.GetProject
	defer func() {
		api.GetProject = originalGetProject
	}()
	api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
		p := mockGitlabProject(projectID)
		return &p, nil
	}

	t.Run("Resolved->head", func(t *testing.T) {
		localRem := rem()

		// Set a head resolution
		localRem.remotes[0].Resolved = "head"

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].RepoHost(), got.RepoHost())
	})

	t.Run("Resolved->head:", func(t *testing.T) {
		localRem := rem()

		expectedResolution := NewWithHost("maxice8", "glab", "gitlab.com")

		// Set a base resolution
		localRem.remotes[0].Resolved = "head: gitlab.com/maxice8/glab"

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, expectedResolution.FullName(), got.FullName())
		assert.Equal(t, expectedResolution.RepoHost(), got.RepoHost())
	})

	t.Run("Resolved->head: (invalid)", func(t *testing.T) {
		localRem := rem()

		// Set a base resolution
		localRem.remotes[0].Resolved = "head:NotAnActualValidValue"

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		assert.Nil(t, got)
		assert.EqualError(t, err, `expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got "NotAnActualValidValue"`)
	})

	t.Run("ResolvedHead->head (new field)", func(t *testing.T) {
		localRem := rem()

		// Set the new ResolvedHead field
		localRem.remotes[0].ResolvedHead = "head"

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].RepoHost(), got.RepoHost())
	})

	t.Run("ResolvedHead->head: (new field with prefix)", func(t *testing.T) {
		localRem := rem()

		expectedResolution := NewWithHost("example", "glab", "gitlab.com")

		// Set the new ResolvedHead field with prefix
		localRem.remotes[0].ResolvedHead = "head: gitlab.com/example/glab"

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, expectedResolution.FullName(), got.FullName())
		assert.Equal(t, expectedResolution.RepoHost(), got.RepoHost())
	})

	t.Run("ResolvedHead takes precedence over Resolved", func(t *testing.T) {
		localRem := rem()

		expectedResolution := NewWithHost("example", "glab", "gitlab.com")

		// Set both fields - new field should win
		localRem.remotes[0].ResolvedHead = "head: gitlab.com/example/glab"
		localRem.remotes[0].Resolved = "head: gitlab.com/different/repo"

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		// Should use ResolvedHead, not Resolved
		assert.Equal(t, expectedResolution.FullName(), got.FullName())
		assert.Equal(t, expectedResolution.RepoHost(), got.RepoHost())
	})

	t.Run("Prompt==false", func(t *testing.T) {
		localRem := rem()

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].FullName(), got.FullName())
	})

	t.Run("Consult the network 1 repo", func(t *testing.T) {
		localRem := rem()

		// Prompt must be true otherwise we won't reach the code we want to test
		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, got.FullName(), localRem.remotes[0].FullName())
	})

	t.Run("Consult the network, more than 1 forked repo, pick the fork", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
			ForkedFromProject: &gitlab.ForkParent{
				ID:                1,
				HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				PathWithNamespace: "profclems/glab",
			},
		}

		localRem.remotes = Remotes{originRemote}
		localRem.network = []gitlab.Project{originNetwork}

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, "maxice8/glab", got.FullName())
	})

	t.Run("Consult the network, more than 1 repo, pick the first", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
		}

		localRem.remotes = append(localRem.remotes, originRemote)
		localRem.network = append(localRem.network, originNetwork)

		got, err := localRem.HeadRepo(t.Context(), iostreams.New())
		require.NoError(t, err)

		assert.Equal(t, "profclems/glab", got.FullName())
	})

	t.Run("Consult the network, no remotes", func(t *testing.T) {
		localRem := rem()

		// Wipe out all remotes
		localRem.remotes = Remotes{}
		localRem.network = nil

		_, err := localRem.HeadRepo(t.Context(), iostreams.New(iostreams.WithStdout(nil, true), iostreams.WithStderr(nil, true)))
		assert.EqualError(t, err, "no GitLab Projects found from remotes")
	})

	t.Run("Consult the network, multiple projects, pick origin", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
		}

		localRem.remotes = append(localRem.remotes, originRemote)
		localRem.network = append(localRem.network, originNetwork)

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the head repository")).
			Do(ugh.SelectIndex(1))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)
		got, err := localRem.HeadRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, originRemote.Repo.FullName(), got.FullName())
		assert.Equal(t, originRemote.Repo.RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network, multiple projects, pick upstream", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
		}

		localRem.remotes = append(localRem.remotes, originRemote)
		localRem.network = append(localRem.network, originNetwork)

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the head repository")).
			Do(ugh.SelectIndex(0))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)
		got, err := localRem.HeadRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, localRem.remotes[0].Repo.FullName(), got.FullName())
		assert.Equal(t, localRem.remotes[0].Repo.RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network, one forked project, get fork", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
			ForkedFromProject: &gitlab.ForkParent{
				ID:                1,
				HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				PathWithNamespace: "profclems/glab",
			},
		}

		localRem.remotes = Remotes{originRemote}
		localRem.network = []gitlab.Project{originNetwork}

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the head repository")).
			Do(ugh.SelectIndex(1))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)
		got, err := localRem.HeadRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, originRemote.Repo.FullName(), got.FullName())
		assert.Equal(t, originRemote.Repo.RepoHost(), got.RepoHost())
	})

	t.Run("Consult the network, one forked project, get upstream", func(t *testing.T) {
		localRem := rem()

		originRemote := &Remote{
			Remote: &git.Remote{Name: "origin"},
			Repo:   NewWithHost("maxice8", "glab", "gitlab.com"),
		}

		originNetwork := gitlab.Project{
			ID:                2,
			PathWithNamespace: "maxice8/glab",
			HTTPURLToRepo:     "https://gitlab.com/maxice8/glab",
			ForkedFromProject: &gitlab.ForkParent{
				ID:                1,
				HTTPURLToRepo:     "https://gitlab.com/profclems/glab",
				PathWithNamespace: "profclems/glab",
			},
		}

		localRem.remotes = Remotes{originRemote}
		localRem.network = []gitlab.Project{originNetwork}

		// Mock the prompt
		c := ugh.New(t)
		c.Expect(ugh.SelectRegexp("Which should be the head repository")).
			Do(ugh.SelectIndex(0))
		ios, cleanup := testIOStreamsWithConsole(t, c)
		t.Cleanup(cleanup)
		got, err := localRem.HeadRepo(t.Context(), ios)
		require.NoError(t, err)

		assert.Equal(t, "profclems/glab", got.FullName())
		assert.Equal(t, "gitlab.com", got.RepoHost())
	})

	t.Run("Host mismatch: API host differs from git remote host", func(t *testing.T) {
		// This test verifies our fix for the host mismatch bug
		// where the API returns a different host than the git remote
		// Git remote uses git.example.com, but API returns api.example.com
		localRem := &ResolvedRemotes{
			remotes: Remotes{
				&Remote{
					Remote: &git.Remote{
						Name: "origin",
					},
					Repo: NewWithHost("owner", "repo", "git.example.com"), // Git remote uses git host
				},
			},
			apiClient: &gitlab.Client{},
			network: []gitlab.Project{
				{
					ID:                1,
					PathWithNamespace: "owner/repo",
					HTTPURLToRepo:     "https://api.example.com/owner/repo.git", // API returns api.example.com, different from git remote's git.example.com
				},
			},
		}

		// Override git.SetRemoteResolution so it doesn't mess with the user configs
		originalSetRemoteResolution := git.SetRemoteResolution
		defer func() {
			git.SetRemoteResolution = originalSetRemoteResolution
		}()
		git.SetRemoteResolution = func(_, _ string) error {
			return nil
		}

		// Override api.GetProject so it doesn't mess with other tests
		originalGetProject := api.GetProject
		defer func() {
			api.GetProject = originalGetProject
		}()
		api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
			p := &gitlab.Project{
				PathWithNamespace: fmt.Sprint(projectID),
				HTTPURLToRepo:     "https://api.example.com/owner/repo.git", // API returns api.example.com, different from git remote's git.example.com
			}
			return p, nil
		}

		ios := iostreams.New(iostreams.WithStdout(io.Discard, true), iostreams.WithStderr(io.Discard, true))

		got, err := localRem.HeadRepo(t.Context(), ios)
		require.NoError(t, err)

		// The fix should ensure we use the git remote host, not the API host
		assert.Equal(t, "owner/repo", got.FullName())
		assert.Equal(t, "git.example.com", got.RepoHost()) // Should use git remote host, not API host
	})
}
