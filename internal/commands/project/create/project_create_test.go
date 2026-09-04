//go:build !integration

package create

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/acarl005/stripansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "project_create_test")
}

func TestProjectPathFromArgs(t *testing.T) {
	t.Parallel()

	const defaultHostname = "git.example.com"

	tests := []struct {
		name          string
		input         string
		wantHost      string
		wantNamespace string
		wantProject   string
	}{
		{
			name:          "shallow owner",
			input:         "owner/project",
			wantHost:      defaultHostname,
			wantNamespace: "owner",
			wantProject:   "project",
		},
		{
			name:          "nested owner",
			input:         "top-level/subgroup/project",
			wantHost:      defaultHostname,
			wantNamespace: "top-level/subgroup",
			wantProject:   "project",
		},
		{
			name:          "tertiary namespace",
			input:         "top-level/subgroup/third-level/project",
			wantHost:      defaultHostname,
			wantNamespace: "top-level/subgroup/third-level",
			wantProject:   "project",
		},
		{
			name:          "tertiary namespace with explicit default host",
			input:         "git.example.com/top-level/subgroup/third-level/project",
			wantHost:      defaultHostname,
			wantNamespace: "top-level/subgroup/third-level",
			wantProject:   "project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, namespace, project := projectPathFromArgs([]string{tt.input}, defaultHostname, config.NewBlankConfig())

			assert.Equal(t, tt.wantHost, host)
			assert.Equal(t, tt.wantNamespace, namespace)
			assert.Equal(t, tt.wantProject, project)
		})
	}
}

func Test_projectCreateCmd(t *testing.T) {
	// Note: Cannot use t.Parallel() because tests modify package-level mock functions
	// Save original functions to restore after tests
	origCreateProject := createProject
	origCurrentUser := currentUser
	origAddRemote := addRemote
	origGitInitializer := gitInitializer
	origRepoInitializer := repoInitializer
	origRepoCloner := repoCloner

	defer func() {
		createProject = origCreateProject
		currentUser = origCurrentUser
		addRemote = origAddRemote
		gitInitializer = origGitInitializer
		repoInitializer = origRepoInitializer
		repoCloner = origRepoCloner
	}()

	testCases := []struct {
		Name           string
		Args           []string
		ExpectedStdout []string
		ExpectedStderr []string
		SetupMocks     func()
		wantErr        bool
	}{
		{
			Name: "Create project with only repo name - success (creates subdirectory)",
			Args: []string{"reponame"},
			ExpectedStdout: []string{
				"Created project on GitLab: reponame -",
			},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						ID:                1,
						Name:              *opts.Name,
						Path:              *opts.Path,
						NameWithNamespace: *opts.Name,
						WebURL:            "https://gitlab.com/username/reponame",
						SSHURLToRepo:      "git@gitlab.com:username/" + *opts.Path + ".git",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{
						ID:       1,
						Username: "username",
						Name:     "name",
					}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return &git.Remote{Name: name}, nil
				}
				gitInitializer = func() error {
					return nil
				}
				repoInitializer = func(projectPath, remoteURL string) error {
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					return nil
				}
			},
		},
		{
			Name: "Create project with slash suffix",
			Args: []string{"reponame/"},
			ExpectedStdout: []string{
				"Created project on GitLab: reponame -",
			},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						ID:                1,
						Name:              *opts.Name,
						Path:              *opts.Path,
						NameWithNamespace: *opts.Name,
						WebURL:            "https://gitlab.com/username/reponame",
						SSHURLToRepo:      "git@gitlab.com:username/" + *opts.Path + ".git",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{
						ID:       1,
						Username: "username",
						Name:     "name",
					}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return &git.Remote{Name: name}, nil
				}
				gitInitializer = func() error {
					return nil
				}
				repoInitializer = func(projectPath, remoteURL string) error {
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					return nil
				}
			},
		},
		{
			Name: "Create project with --skipGitInit flag",
			Args: []string{"test-repo", "--skipGitInit"},
			ExpectedStdout: []string{
				"Created project on GitLab: test-repo -",
			},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						ID:                1,
						Name:              *opts.Name,
						Path:              *opts.Path,
						NameWithNamespace: *opts.Name,
						WebURL:            "https://gitlab.com/username/test-repo",
						SSHURLToRepo:      "git@gitlab.com:username/" + *opts.Path + ".git",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{
						ID:       1,
						Username: "username",
						Name:     "name",
					}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return &git.Remote{Name: name}, nil
				}
				gitInitializer = func() error {
					return nil
				}
				repoInitializer = func(projectPath, remoteURL string) error {
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					return nil
				}
			},
		},
		{
			Name: "GitLab API fails - fatal error",
			Args: []string{"failing-repo"},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return nil, errors.New("API error")
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{
						ID:       1,
						Username: "username",
						Name:     "name",
					}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return &git.Remote{Name: name}, nil
				}
				gitInitializer = func() error {
					return nil
				}
				repoInitializer = func(projectPath, remoteURL string) error {
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					return nil
				}
			},
			wantErr: true, // API failures should error
		},
		{
			Name: "GitLab returns invalid project web URL",
			Args: []string{"invalid-url-repo"},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						NameWithNamespace: "username/invalid-url-repo",
						WebURL:            "http://[::1",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{ID: 1, Username: "username"}, nil
				}
			},
			wantErr: true,
		},
		{
			Name: "Create project with name - NO_PROMPT does not create subdirectory by default",
			Args: []string{"new-project"},
			ExpectedStdout: []string{
				"Created project on GitLab: username/new-project -",
			},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						ID:                1,
						Name:              *opts.Name,
						Path:              *opts.Path,
						NameWithNamespace: "username/" + *opts.Name,
						WebURL:            "https://gitlab.com/username/" + *opts.Name,
						SSHURLToRepo:      "git@gitlab.com:username/" + *opts.Path + ".git",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{
						ID:       1,
						Username: "username",
						Name:     "name",
					}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return &git.Remote{Name: name}, nil
				}
				gitInitializer = func() error {
					return nil
				}
				repoInitializer = func(projectPath, remoteURL string) error {
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					return nil
				}
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// Note: Cannot use t.Parallel() here because tests share and modify package-level mocks
			// Setup mocks for this test
			tc.SetupMocks()

			io, _, stdout, stderr := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
				hosts:
				  gitlab.com:
				    username: monalisa
				    token: OTOKEN
				no_prompt: true
			`))))

			cmd := NewCmdCreate(f)
			cmdutils.EnableRepoOverride(cmd, f)
			cmd.SetArgs(tc.Args)

			_, err := cmd.ExecuteC()

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			out := stripansi.Strip(stdout.String())
			errOut := stripansi.Strip(stderr.String())

			for _, msg := range tc.ExpectedStdout {
				assert.Contains(t, out, msg, "Expected stdout to contain: %s", msg)
			}

			for _, msg := range tc.ExpectedStderr {
				assert.Contains(t, errOut, msg, "Expected stderr to contain: %s", msg)
			}
		})
	}
}

// Test_projectCreateCmd_InCurrentDirectory tests the scenario where we run glab repo create
// without arguments, which creates the project in the current directory and runs git operations
func Test_projectCreateCmd_InCurrentDirectory(t *testing.T) {
	// Note: Cannot use t.Parallel() because tests modify package-level mock functions
	// Save original functions to restore after tests
	origCreateProject := createProject
	origCurrentUser := currentUser
	origAddRemote := addRemote
	origGitInitializer := gitInitializer
	origRepoCloner := repoCloner

	defer func() {
		createProject = origCreateProject
		currentUser = origCurrentUser
		addRemote = origAddRemote
		gitInitializer = origGitInitializer
		repoCloner = origRepoCloner
	}()

	testCases := []struct {
		Name           string
		Args           []string
		ExpectedStdout []string
		ExpectedStderr []string
		SetupMocks     func()
		wantErr        bool
	}{
		{
			Name: "Create project in current dir - remote already exists (main bug fix)",
			ExpectedStdout: []string{
				"Created project on GitLab:",
			},
			ExpectedStderr: []string{
				"Warning: Could not add remote: remote origin already exists",
			},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						ID:                1,
						Name:              "test-project",
						NameWithNamespace: "username/test-project",
						WebURL:            "https://gitlab.com/username/test-project",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{
						ID:       1,
						Username: "username",
						Name:     "name",
					}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return nil, errors.New("remote origin already exists. git: exit status 3")
				}
				gitInitializer = func() error {
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					return nil
				}
			},
			wantErr: false, // Should not error, just warn
		},
		{
			Name: "Create project in current dir (already git init'd) - add remote succeeds",
			ExpectedStdout: []string{
				"Created project on GitLab:",
				"Added remote",
			},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						ID:                1,
						Name:              "test-project",
						NameWithNamespace: "username/test-project",
						WebURL:            "https://gitlab.com/username/test-project",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{
						ID:       1,
						Username: "username",
						Name:     "name",
					}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return &git.Remote{Name: name}, nil
				}
				gitInitializer = func() error {
					// Should not be called since we're already in a git repo
					t.Error("gitInitializer should not be called when already in a git repository")
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					t.Error("repoCloner should not be called when already in a git repository")
					return nil
				}
			},
			wantErr: false,
		},
		{
			Name: "Create project with --readme (already git init'd) - adds remote only",
			Args: []string{"--readme"},
			ExpectedStdout: []string{
				"Created project on GitLab:",
				"Added remote",
			},
			SetupMocks: func() {
				createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
					return &gitlab.Project{
						ID:                1,
						Name:              "test-project",
						NameWithNamespace: "username/test-project",
						WebURL:            "https://gitlab.com/username/test-project",
						SSHURLToRepo:      "git@gitlab.com:username/test-project.git",
					}, nil
				}
				currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
					return &gitlab.User{ID: 1, Username: "username", Name: "name"}, nil
				}
				addRemote = func(name, url string) (*git.Remote, error) {
					return &git.Remote{Name: name}, nil
				}
				gitInitializer = func() error {
					t.Error("gitInitializer should not be called when already in a git repository")
					return nil
				}
				repoCloner = func(cloneURL, target, remoteName string) error {
					t.Error("repoCloner should not be called when already in a git repository")
					return nil
				}
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// Note: Cannot use t.Parallel() here because tests share and modify package-level mocks
			// Setup mocks for this test
			tc.SetupMocks()

			io, _, stdout, stderr := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
				hosts:
				  gitlab.com:
				    username: monalisa
				    token: OTOKEN
				no_prompt: true
			`))))

			cmd := NewCmdCreate(f)
			cmdutils.EnableRepoOverride(cmd, f)
			args := tc.Args
			if args == nil {
				args = []string{}
			}
			cmd.SetArgs(args)

			_, err := cmd.ExecuteC()

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			out := stripansi.Strip(stdout.String())
			errOut := stripansi.Strip(stderr.String())

			for _, msg := range tc.ExpectedStdout {
				assert.Contains(t, out, msg, "Expected stdout to contain: %s", msg)
			}

			for _, msg := range tc.ExpectedStderr {
				assert.Contains(t, errOut, msg, "Expected stderr to contain: %s", msg)
			}
		})
	}
}

// --skipGitInit promises to skip "both 'git init' and cloning". Two paths did not
// honour it, and they fail differently depending on whether a project name is given,
// so both are covered here.
func Test_projectCreateCmd_SkipGitInit(t *testing.T) {
	// Cannot use t.Parallel(): these tests replace package-level mocks and chdir.
	origCreateProject := createProject
	origCurrentUser := currentUser
	origAddRemote := addRemote
	origGitInitializer := gitInitializer
	origRepoCloner := repoCloner
	origRepoInitializer := repoInitializer

	t.Cleanup(func() {
		createProject = origCreateProject
		currentUser = origCurrentUser
		addRemote = origAddRemote
		gitInitializer = origGitInitializer
		repoCloner = origRepoCloner
		repoInitializer = origRepoInitializer
	})

	stubAPI := func() {
		createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
			return &gitlab.Project{
				ID:                1,
				Name:              "test-project",
				Path:              "test-project",
				NameWithNamespace: "username/test-project",
				WebURL:            "https://gitlab.com/username/test-project",
			}, nil
		}
		currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
			return &gitlab.User{ID: 1, Username: "username", Name: "name"}, nil
		}
	}

	t.Run("no project name, not a Git repository: does not touch the remote", func(t *testing.T) {
		// An empty temp dir makes `git rev-parse --git-dir` fail, so isGitInitialized
		// is false — the exact situation in which `git remote add` printed
		// "fatal: not a git repository" while the command still exited 0.
		t.Chdir(t.TempDir())

		stubAPI()
		addRemoteCalled := false
		addRemote = func(name, url string) (*git.Remote, error) {
			addRemoteCalled = true
			return nil, errors.New("fatal: not a git repository")
		}
		gitInitCalled := false
		gitInitializer = func() error {
			gitInitCalled = true
			return nil
		}

		io, _, stdout, stderr := cmdtest.TestIOStreams()
		f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
			hosts:
			  gitlab.com:
			    username: monalisa
			    token: OTOKEN
			no_prompt: true
		`))))

		cmd := NewCmdCreate(f)
		cmdutils.EnableRepoOverride(cmd, f)
		cmd.SetArgs([]string{"--skipGitInit"})

		_, err := cmd.ExecuteC()
		require.NoError(t, err)

		assert.False(t, addRemoteCalled, "addRemote must not run outside a Git repository")
		assert.False(t, gitInitCalled, "--skipGitInit must not run git init")
		assert.Contains(t, stripansi.Strip(stdout.String()), "Created project on GitLab:")
		assert.NotContains(t, stripansi.Strip(stderr.String()), "Could not add remote")
	})

	t.Run("with a project name: no prompt and no local setup", func(t *testing.T) {
		t.Chdir(t.TempDir())

		stubAPI()
		addRemote = func(name, url string) (*git.Remote, error) {
			return nil, errors.New("addRemote should not be reached")
		}
		clonerCalled := false
		repoCloner = func(cloneURL, target, remoteName string) error {
			clonerCalled = true
			return nil
		}
		initializerCalled := false
		repoInitializer = func(projectPath, remote string) error {
			initializerCalled = true
			return nil
		}

		// A TTY with prompts enabled is what made the old code prompt and then run
		// the local setup that --skipGitInit says it skips.
		io, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
		f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
			hosts:
			  gitlab.com:
			    username: monalisa
			    token: OTOKEN
		`))))

		cmd := NewCmdCreate(f)
		cmdutils.EnableRepoOverride(cmd, f)
		cmd.SetArgs([]string{"test-project", "--skipGitInit"})

		// The prompt reads from the command's context. With --skipGitInit honoured
		// nothing blocks and this finishes in milliseconds; if the flag is ignored
		// the confirm waits on a TTY nobody is typing into, so the deadline turns
		// what would otherwise be a hung CI job into a plain failure here.
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		_, err := cmd.ExecuteContextC(ctx)
		require.NoError(t, err)
		require.NoError(t, ctx.Err(), "--skipGitInit still prompted: the confirm blocked until the deadline")

		assert.False(t, clonerCalled, "--skipGitInit must not clone")
		assert.False(t, initializerCalled, "--skipGitInit must not initialize a local repository")
		assert.Contains(t, stripansi.Strip(stdout.String()), "Created project on GitLab:")
		assert.NotContains(t, stripansi.Strip(stdout.String()), "Initialized repository")
	})
}
