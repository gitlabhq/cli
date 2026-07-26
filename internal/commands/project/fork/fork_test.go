//go:build !integration

package fork

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestProjectFork(t *testing.T) {
	cloneShelloutStubs := []string{
		"git clone git@gitlab.com:OWNER/baz.git REPO",
		"git -C REPO remote add -f upstream git@gitlab.com:OWNER/REPO.git",
	}

	expectedCloneShellouts := []string{
		"git clone ",
		"git -C . remote add -f upstream git@gitlab.com:OWNER/REPO.git",
	}

	tests := []struct {
		name              string
		commandArgs       string
		shelloutStubs     []string
		expectedShellouts []string
		expectClonePrompt bool
		expectClone       bool
	}{
		{
			name:              "when a specified repository is forked and cloned",
			commandArgs:       "OWNER/REPO --name foo --path baz --clone",
			shelloutStubs:     cloneShelloutStubs,
			expectedShellouts: expectedCloneShellouts,
			expectClonePrompt: false,
			expectClone:       true,
		},
		{
			name:              "when a specified repository is forked user is prompted to clone",
			commandArgs:       "OWNER/REPO --name foo --path baz",
			shelloutStubs:     cloneShelloutStubs,
			expectedShellouts: expectedCloneShellouts,
			expectClonePrompt: true,
			expectClone:       true,
		},
		{
			name:              "when a specified repository is forked and clone is set to false",
			commandArgs:       "OWNER/REPO --name foo --path baz --clone=false",
			shelloutStubs:     []string{},
			expectedShellouts: []string{},
			expectClonePrompt: false,
			expectClone:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, csTeardown := test.InitCmdStubber()
			defer csTeardown()

			for _, stub := range tt.shelloutStubs {
				cs.Stub(stub)
			}

			tc := gitlabtesting.NewTestClient(t)
			tc.MockProjects.EXPECT().
				ForkProject("OWNER/REPO", gomock.Any(), gomock.Any()).
				Return(&gitlab.Project{
					ID:                99,
					PathWithNamespace: "OWNER/baz",
				}, nil, nil)
			if tt.expectClone {
				tc.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(&gitlab.Project{
						ID:                100,
						Description:       "this is a test description",
						Name:              "foo",
						NameWithNamespace: "OWNER / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "main",
						HTTPURLToRepo:     "https://gitlab.com/OWNER/REPO.git",
						SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
					}, nil, nil)
			}

			opts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithApiClient(
					cmdtest.NewTestApiClient(
						t,
						nil,
						"",
						glinstance.DefaultHostname,
						api.WithGitLabClient(tc.Client),
					),
				),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
				func(f *cmdtest.Factory) {
					f.RemotesStub = func() (glrepo.Remotes, error) {
						remote := &git.Remote{
							Name: "origin",
							FetchURL: &url.URL{
								Scheme: "https",
								Host:   "gitlab.com",
								Path:   "/OWNER/REPO.git",
							},
							PushURL: &url.URL{
								Scheme: "https",
								Host:   "gitlab.com",
								Path:   "/OWNER/REPO.git",
							},
						}

						repo := glrepo.New("OWNER", "REPO", glinstance.DefaultHostname)

						return glrepo.Remotes{
							&glrepo.Remote{
								Remote: remote,
								Repo:   repo,
							},
						}, nil
					}
				},
			}

			// Set up prompt stub if needed
			if tt.expectClonePrompt {
				c := ugh.New(t)
				c.Expect(ugh.Confirm("Would you like to clone the fork?")).
					Do(ugh.Affirm)
				opts = append(opts, cmdtest.WithConsole(t, c))
			}

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdFork,
				false,
				opts...,
			)

			out, err := exec(tt.commandArgs)

			if assert.NoErrorf(
				t,
				err,
				"error running command `project fork %s`: %v",
				tt.commandArgs,
				err,
			) {
				assert.Equal(t, "✓ Created fork OWNER/baz.\n", out.ErrBuf.String())
			}

			assert.Equal(t, len(tt.expectedShellouts), cs.Count)
			for idx, expectedShellout := range tt.expectedShellouts {
				assert.Equal(t, expectedShellout, strings.Join(cs.Calls[idx].Args, " "))
			}
		})
	}
}

func TestProjectForkReturnsTransportErrorWithoutResponse(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjects.EXPECT().
		ForkProject("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("network unavailable"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdFork,
		false,
		cmdtest.WithApiClient(
			cmdtest.NewTestApiClient(
				t,
				nil,
				"",
				glinstance.DefaultHostname,
				api.WithGitLabClient(tc.Client),
			),
		),
	)

	_, err := exec("OWNER/REPO --clone=false")
	require.EqualError(t, err, "network unavailable")
}

func TestProjectForkExistingRepo(t *testing.T) {
	shelloutStubs := []string{
		"git remote rename executed",
		"git remote add executed",
	}

	expectedShellouts := []string{
		"git remote rename origin upstream",
		"git remote add -f origin git@gitlab.com:OWNER/REPO.git",
	}

	tests := []struct {
		name                   string
		commandArgs            string
		shelloutStubs          []string
		expectedShellouts      []string
		addRemoteFlag          bool
		promptResponse         bool
		expectError            bool
		expectNamespaceMessage bool
	}{
		{
			name:                   "when fork exists and user wants to add a remote",
			commandArgs:            "", // Empty to simulate running in current directory
			shelloutStubs:          shelloutStubs,
			expectedShellouts:      expectedShellouts,
			promptResponse:         true,
			expectError:            false,
			expectNamespaceMessage: false,
		},
		{
			name:                   "when fork exists and remote flag is true",
			commandArgs:            "--remote",
			shelloutStubs:          shelloutStubs,
			expectedShellouts:      expectedShellouts,
			addRemoteFlag:          true,
			expectError:            false,
			expectNamespaceMessage: false,
		},
		{
			name:                   "when fork exists and remote flag is false",
			commandArgs:            "--remote=false",
			shelloutStubs:          []string{},
			expectedShellouts:      []string{},
			addRemoteFlag:          false,
			expectError:            false,
			expectNamespaceMessage: false,
		},
		{
			name:                   "when fork exists but project not found in user namespace (should error)",
			commandArgs:            "--remote",
			shelloutStubs:          []string{}, // No shellouts expected because operation errors out before git commands
			expectedShellouts:      []string{},
			addRemoteFlag:          true,
			expectError:            true,
			expectNamespaceMessage: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize git repository for testing
			git.InitGitRepo(t)
			// tempDir := cmdtest.InitGitRepo(t, "gitlab.com", "OWNER", "REPO")

			cs, csTeardown := test.InitCmdStubber()
			defer csTeardown()
			for _, stub := range tt.shelloutStubs {
				cs.Stub(stub)
			}

			tc := gitlabtesting.NewTestClient(t)
			tc.MockUsers.EXPECT().CurrentUser().Return(&gitlab.User{
				Username:    "OWNER",
				ID:          123,
				Name:        "Test User",
				NamespaceID: 123,
			}, nil, nil).AnyTimes()
			tc.MockProjects.EXPECT().
				ForkProject("OWNER/REPO", gomock.Any(), gomock.Any()).
				Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusConflict}}, errors.New(`{"message":"Project namespace name has already been taken"}`))

			if tt.promptResponse || tt.addRemoteFlag {
				// Register the projects search API that happens during the "repository already exists" flow
				if tt.expectNamespaceMessage {
					// Return an empty list to simulate no matching project in user's namespace
					tc.MockProjects.EXPECT().
						ListProjects(gomock.Any(), gomock.Any()).
						Return(nil, nil, nil)
				} else {
					tc.MockProjects.EXPECT().ListProjects(gomock.Any(), gomock.Any()).Return([]*gitlab.Project{
						{
							ID:                123,
							Name:              "REPO",
							Description:       "Test repo",
							Path:              "REPO",
							PathWithNamespace: "OWNER/REPO",
							// Created_at": "2023-01-01T00:00:00Z",
							DefaultBranch: "main",
							SSHURLToRepo:  "git@gitlab.com:OWNER/REPO.git",
							HTTPURLToRepo: "https://gitlab.com/OWNER/REPO.git",
							WebURL:        "https://gitlab.com/OWNER/REPO",
							Namespace: &gitlab.ProjectNamespace{
								ID:       123,
								Name:     "OWNER",
								Path:     "OWNER",
								Kind:     "user",
								FullPath: "OWNER",
							},
						},
					}, nil, nil)
				}
			}

			opts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithApiClient(
					cmdtest.NewTestApiClient(
						t,
						nil,
						"",
						glinstance.DefaultHostname,
						api.WithGitLabClient(tc.Client),
					),
				),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
				func(f *cmdtest.Factory) {
					f.RemotesStub = func() (glrepo.Remotes, error) {
						remote := &git.Remote{
							Name: "origin",
							FetchURL: &url.URL{
								Scheme: "https",
								Host:   "gitlab.com",
								Path:   "/OWNER/REPO.git",
							},
							PushURL: &url.URL{
								Scheme: "https",
								Host:   "gitlab.com",
								Path:   "/OWNER/REPO.git",
							},
						}

						repo := glrepo.New("OWNER", "REPO", glinstance.DefaultHostname)

						return glrepo.Remotes{
							&glrepo.Remote{
								Remote: remote,
								Repo:   repo,
							},
						}, nil
					}
				},
			}
			// Set up prompt stub if needed
			if tt.promptResponse {
				c := ugh.New(t)
				c.Expect(ugh.Confirm("Would you like to add this repository as a remote instead?")).
					Do(ugh.Affirm)
				opts = append(opts, cmdtest.WithConsole(t, c))
			}

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdFork,
				true,
				opts...,
			)

			t.Logf("Running fork command with arguments: %q", tt.commandArgs)
			out, err := exec(tt.commandArgs)

			// Log stderr for debugging

			if tt.expectError {
				require.Error(t, err, "expected an error but got none")
				if tt.expectNamespaceMessage {
					assert.Contains(t, out.ErrBuf.String(), "Only user namespaces")
				}
			} else {
				if assert.NoErrorf(t, err, "error running command `project fork %s`: %v", tt.commandArgs, err) {
					// On success, ensure namespace error message is absent unless we expect it
					if !tt.expectNamespaceMessage {
						assert.NotContains(t, out.ErrBuf.String(), "Only user namespaces")
					}

					// Check success related messages
					if tt.addRemoteFlag || tt.promptResponse {
						assert.Contains(t, out.ErrBuf.String(), "Using existing repository")
						if len(tt.expectedShellouts) > 0 {
							assert.Contains(t, out.ErrBuf.String(), "Added remote")
						}
					}
				}
			}

			// Assert shellouts
			if len(tt.expectedShellouts) > 0 {
				require.Equal(t, len(tt.expectedShellouts), cs.Count)
				for idx, expectedShellout := range tt.expectedShellouts {
					assert.Equal(t, expectedShellout, strings.Join(cs.Calls[idx].Args, " "))
				}
			} else {
				assert.Equal(t, 0, cs.Count)
			}
		})
	}
}
