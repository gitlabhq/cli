//go:build !integration

package create

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestNewCmdCreate_tty(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListMilestones
	testClient.MockMilestones.EXPECT().
		ListMilestones("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Milestone{
			{
				ID:          1,
				IID:         3,
				Description: "foo",
			},
		}, nil, nil)

	// Mock ListProjectTargetBranchRules (no rules configured)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	// Mock ListUsers
	testClient.MockUsers.EXPECT().
		ListUsers(gomock.Any()).
		Return([]*gitlab.User{
			{
				Username: "testuser",
			},
		}, nil, nil)

	// Mock CreateMergeRequest
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:           1,
				IID:          12,
				ProjectID:    3,
				Title:        "myMRtitle",
				Description:  "myMRbody",
				State:        "opened",
				TargetBranch: "master",
				SourceBranch: "feat-new-mr",
				WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
			},
		}, nil, nil)

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"-t", "myMRtitle",
		"-d", "myMRbody",
		"-l", "test,bug",
		"--milestone", "foo",
		"--assignee", "testuser",
	}

	cli := strings.Join(cliStr, " ")

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}

	outputLines := strings.SplitN(output.String(), "\n", 2)
	assert.Contains(t, outputLines[0], "!12 myMRtitle (feat-new-mr)")
	assert.Contains(t, output.Stderr(), "\nCreating merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestNewCmdCreate_RelatedIssue(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListProjectTargetBranchRules (no rules configured)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	// Mock GetIssue
	testClient.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.Issue{
			ID:          1,
			IID:         1,
			ProjectID:   1,
			Title:       "this is a issue title",
			Description: "issue description",
		}, nil, nil)

	// Mock CreateMergeRequest and verify the title and description
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Contains(t, *opts.Title, `Draft: Resolve "this is a issue title"`)
			assert.Contains(t, *opts.Description, "\n\nCloses #1")
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        `Draft: Resolve "this is a issue title"`,
					Description:  "\n\nCloses #1",
					State:        "opened",
					TargetBranch: "master",
					SourceBranch: "feat-new-mr",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
			deadbeef HEAD
			deadb00f refs/remotes/upstream/feat-new-mr
			deadbeef refs/remotes/origin/feat-new-mr
		`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"--related-issue", "1",
		"--source-branch", "feat-new-mr",
		"--yes",
	}

	cli := strings.Join(cliStr, " ")

	t.Log(cli)

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}
	outputLines := strings.SplitN(output.String(), "\n", 2)
	assert.Contains(t, outputLines[0], `!12 Draft: Resolve "this is a issue title" (feat-new-mr)`)
	assert.Contains(t, output.Stderr(), "\nCreating draft merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestNewCmdCreate_TemplateFromCommitMessages(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListProjectTargetBranchRules (no rules configured)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	// Mock CreateMergeRequest and verify the description contains commit messages
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Contains(t, *opts.Description, "- commit msg 1  \n\n")
			assert.Contains(t, *opts.Description, "- commit msg 2  \ncommit body")
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        "...",
					Description:  "...",
					State:        "opened",
					TargetBranch: "master",
					SourceBranch: "feat-new-mr",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()

	cs.Stub("/") // git rev-parse --show-toplevel

	// git -c log.ShowSignature=false log --pretty=format:%H,%s --cherry upstream/master...feat-new-mr
	cs.Stub(heredoc.Doc(`
			deadb00f,commit msg 2
			deadbeef,commit msg 1
		`))

	// git -c log.ShowSignature=false show -s --pretty=format:%b deadbeef
	cs.Stub("")
	// git -c log.ShowSignature=false show -s --pretty=format:%b deadb00f
	cs.Stub("commit body")

	c := ugh.New(t)
	c.Expect(ugh.Select("Choose a template:")).
		Do(ugh.SelectIndex(0))
	c.Expect(ugh.Input("Description")).
		Do(ugh.Type("- commit msg 1  \n\n- commit msg 2  \ncommit body"))

	cliStr := []string{
		"--source-branch", "feat-new-mr",
		"--title", "mr-title",
		"--yes",
	}

	cli := strings.Join(cliStr, " ")

	t.Log(cli)

	// Use SetupCmdForTest pattern for responder
	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		// Set up factory with remotes stub
		tf, ok := f.(*cmdtest.Factory)
		require.True(t, ok, "expected cmdtest.Factory")
		tf.RemotesStub = func() (glrepo.Remotes, error) {
			return glrepo.Remotes{
				{
					Remote: &git.Remote{
						Name:     "upstream",
						Resolved: "head",
						PushURL:  pu,
					},
					Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
				},
				{
					Remote: &git.Remote{
						Name:     "origin",
						Resolved: "head",
						PushURL:  pu,
					},
					Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
				},
			}, nil
		}
		tf.BranchStub = func() (string, error) {
			return "feat-new-mr", nil
		}

		return NewCmdCreate(f)
	}, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
		cmdtest.WithConsole(t, c),
	)

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}
}

func TestNewCmdCreate_RelatedIssueWithTitleAndDescription(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListProjectTargetBranchRules (no rules configured)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	// Mock GetIssue
	testClient.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.Issue{
			ID:          1,
			IID:         1,
			ProjectID:   1,
			Title:       "this is a issue title",
			Description: "issue description",
		}, nil, nil)

	// Mock CreateMergeRequest and verify the title and description
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "Draft: my custom MR title", *opts.Title)
			assert.Contains(t, *opts.Description, "my custom MR description\n\nCloses #1")
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        "my custom MR title",
					Description:  "myMRbody",
					State:        "opened",
					TargetBranch: "master",
					SourceBranch: "feat-new-mr",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
			deadbeef HEAD
			deadb00f refs/remotes/upstream/feat-new-mr
			deadbeef refs/remotes/origin/feat-new-mr
		`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"--title", `"my custom MR title"`,
		"--description", `"my custom MR description"`,
		"--related-issue", "1",
		"--source-branch", "feat-new-mr",
	}

	cli := strings.Join(cliStr, " ")

	t.Log(cli)

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}

	outputLines := strings.SplitN(output.String(), "\n", 2)
	assert.Contains(t, outputLines[0], "!12 my custom MR title (feat-new-mr)")
	assert.Contains(t, output.Stderr(), "\nCreating draft merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestMRCreate_nontty_insufficient_flags(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "test-br", nil
			}
		},
	)

	_, err := exec("")
	require.Error(t, err)
	assert.Equal(t, "--title or --fill required for non-interactive mode", err.Error())
}

func TestMRCreate_nontty_titleDoesNotRequireDescription(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(nil, nil, errors.New("stop after validation"))
	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{Name: "upstream", Resolved: "head", PushURL: pu},
						Repo:   glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{Name: "origin", Resolved: "base", PushURL: pu},
						Repo:   glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "test-br", nil
			}
		},
	)

	_, err := exec("--title test-title")
	require.EqualError(t, err, "stop after validation")
}

func TestMrBodyAndTitle(t *testing.T) {
	opts := &options{
		SourceBranch:         "mr-autofill-test-br",
		TargetBranch:         "master",
		TargetTrackingBranch: "origin/master",
	}
	t.Run("", func(t *testing.T) {
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()
		cs.Stub("d1sd2e,docs: add some changes to txt file")                           // git log
		cs.Stub("Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n") // git log

		if err := mrBodyAndTitle(opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "docs: add some changes to txt file", opts.Title)
		assert.Equal(t, "Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n", opts.Description)
	})
	t.Run("given-title", func(t *testing.T) {
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()

		cs.Stub("d1sd2e,docs: add some changes to txt file")
		cs.Stub("Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n") // git log

		opts := *opts
		opts.Title = "docs: make some other stuff"
		if err := mrBodyAndTitle(&opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "docs: make some other stuff", opts.Title)
		assert.Equal(t, `Here, I am adding some commit body.
Little longer

Resolves #1
`, opts.Description)
	})
	t.Run("given-description", func(t *testing.T) {
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()

		cs.Stub("d1sd2e,docs: add some changes to txt file")

		opts := *opts
		opts.Description = `Make it multiple lines
like this

resolves #1
`
		if err := mrBodyAndTitle(&opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "docs: add some changes to txt file", opts.Title)
		assert.Equal(t, `Make it multiple lines
like this

resolves #1
`, opts.Description)
	})
	t.Run("given-fill-commit-body", func(t *testing.T) {
		opts = &options{
			SourceBranch:         "mr-autofill-test-br",
			TargetBranch:         "master",
			TargetTrackingBranch: "origin/master",
		}
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()

		cs.Stub("d1sd2e,chore: some tidying\nd2asa3,docs: more changes to more things")
		cs.Stub("Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n")
		cs.Stub("another body for another commit\ncloses 1234\n")

		opts := *opts
		opts.FillCommitBody = true

		if err := mrBodyAndTitle(&opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "mr autofill test br", opts.Title)
		// Note: trailing spaces (markdown line breaks) are added to certain lines
		assert.Equal(t, "- docs: more changes to more things  \nHere, I am adding some commit body.\nLittle longer  \nResolves #1\n\n- chore: some tidying  \nanother body for another commit\ncloses 1234\n\n", opts.Description)
	})
}

func TestGenerateMRCompareURL(t *testing.T) {
	opts := &options{
		Labels:        []string{"backend", "frontend"},
		Assignees:     []string{"johndoe", "janedoe"},
		Reviewers:     []string{"user", "person"},
		Milestone:     15,
		TargetProject: &gitlab.Project{ID: 100},
		SourceProject: &gitlab.Project{
			ID:     101,
			WebURL: "https://gitlab.example.com/gitlab-org/gitlab",
		},
		Title:        "Autofill tests | for this @project",
		SourceBranch: "@|calc",
		TargetBranch: "project/my-branch",
	}

	u, err := generateMRCompareURL(opts)

	expectedUrl := "https://gitlab.example.com/gitlab-org/gitlab/-/merge_requests/new?" +
		"merge_request%5Bdescription%5D=%0A%2Flabel+~backend%2C+~frontend%0A%2Fassign+johndoe%2C+janedoe%0A%2Freviewer+user%2C+person%0A%2Fmilestone+%2515&" +
		"merge_request%5Bsource_branch%5D=%40%7Ccalc&merge_request%5Bsource_project_id%5D=101&merge_request%5Btarget_branch%5D=project%2Fmy-branch&merge_request%5Btarget_project_id%5D=100&" +
		"merge_request%5Btitle%5D=Autofill+tests+%7C+for+this+%40project"

	require.NoError(t, err)
	assert.Equal(t, expectedUrl, u)
}

func Test_MRCreate_With_Recover_Integration(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// For the first run: GitLabClientStub returns error to trigger recovery file creation
	// For the second run: Normal API mocks

	// Mock GetProject (only called on recovery run - first run fails before API call)
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListProjectTargetBranchRules (called on recovery run)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	// Mock ListUsers (called on recovery)
	testClient.MockUsers.EXPECT().
		ListUsers(gomock.Any()).
		Return([]*gitlab.User{
			{
				Username: "testuser",
			},
		}, nil, nil)

	// Mock CreateMergeRequest (called on recovery)
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:           1,
				IID:          12,
				ProjectID:    3,
				Title:        "myMRtitle",
				Description:  "myMRbody",
				State:        "opened",
				TargetBranch: "master",
				SourceBranch: "feat-new-mr",
				WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
			},
		}, nil, nil)

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))
	// For recovery run
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	cliStr := []string{
		"-t", "myMRtitle",
		"-d", "myMRbody",
		"-l", "test,bug",
		"--milestone", "1",
		"--assignee", "testuser",
	}

	cli := strings.Join(cliStr, " ")

	// First run - let it fail to create recovery file
	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
			// Return error to trigger recovery file creation
			f.GitLabClientStub = func() (*gitlab.Client, error) {
				return nil, errors.New("fail on purpose")
			}
		},
	)

	output, err := exec(cli)

	outErr := output.Stderr()

	require.Errorf(t, err, "fail on purpose")
	require.Contains(t, outErr, "Failed to create merge request. Created recovery file: ")

	// Run create issue with recover
	newCliStr := append(cliStr, "--recover")

	newCli := strings.Join(newCliStr, " ")

	// Second run - recover from file
	exec2 := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	newOutput, newErr := exec2(newCli)
	if newErr != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", newOutput.Stderr())
		}
		t.Error(newErr)
		return
	}

	outputLines := strings.SplitN(newOutput.String(), "\n", 2)
	require.NoError(t, newErr)
	assert.Contains(t, outputLines[0], "Recovered create options from file")
	assert.Contains(t, newOutput.String(), "!12 myMRtitle (feat-new-mr)")
	assert.Contains(t, newOutput.Stderr(), "\nCreating merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, newOutput.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestMRCreate_NoGitRepo_FlagCompleteSucceeds(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "main",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil).AnyTimes()

	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:           1,
				IID:          12,
				Title:        "Test",
				Description:  "TestDesc",
				State:        "opened",
				TargetBranch: "main",
				SourceBranch: "test-branch",
				WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
			},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t,
		func(f cmdutils.Factory) *cobra.Command {
			tf, ok := f.(*cmdtest.Factory)
			require.True(t, ok, "expected cmdtest.Factory")

			// Mirrors what git.Remotes() returns outside a repo: git's own
			// (localized) message, labelled with git.ErrNotAGitRepository.
			tf.RemotesStub = func() (glrepo.Remotes, error) {
				return nil, fmt.Errorf("%w: fatal: not a git repository (or any of the parent directories): .git", git.ErrNotAGitRepository)
			}
			tf.BranchStub = func() (string, error) {
				return "", errors.New("fatal: not a git repository (or any of the parent directories): .git")
			}

			return NewCmdCreate(f)
		},
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	cli := "--source-branch test-branch --target-branch main --title Test --description TestDesc --no-editor --yes"
	output, err := exec(cli)

	require.NoError(t, err, "flag-complete mr create should succeed outside a git repo")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
	assert.Contains(t, output.Stderr(), "Creating merge request for test-branch into main in OWNER/REPO")
}

func TestMRCreate_PushOutsideGitRepo_PropagatesError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "main",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil).AnyTimes()

	exec := cmdtest.SetupCmdForTest(t,
		func(f cmdutils.Factory) *cobra.Command {
			tf, ok := f.(*cmdtest.Factory)
			require.True(t, ok, "expected cmdtest.Factory")

			// Mirrors what git.Remotes() returns outside a repo: git's own
			// (localized) message, labelled with git.ErrNotAGitRepository.
			tf.RemotesStub = func() (glrepo.Remotes, error) {
				return nil, fmt.Errorf("%w: fatal: not a git repository (or any of the parent directories): .git", git.ErrNotAGitRepository)
			}
			tf.BranchStub = func() (string, error) {
				return "", errors.New("fatal: not a git repository (or any of the parent directories): .git")
			}

			return NewCmdCreate(f)
		},
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	cli := "--source-branch test-branch --target-branch main --title Test --description TestDesc --no-editor --yes --push"
	output, err := exec(cli)

	require.Error(t, err, "expected error when --push is used outside a git repo")
	assert.Contains(t, err.Error(), "not a git repository", "error should mention git repository")
	assert.NotContains(t, output.String(), "/-/merge_requests/", "should not have created a merge request")
}

func TestMRCreate_RemotesResolverError_PropagatesError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "main",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil).AnyTimes()

	resolverErr := errors.New("none of the git remotes configured for this repository correspond to the GITLAB_HOST environment variable. Try adding a matching remote or unsetting the variable.")

	exec := cmdtest.SetupCmdForTest(t,
		func(f cmdutils.Factory) *cobra.Command {
			tf, ok := f.(*cmdtest.Factory)
			require.True(t, ok, "expected cmdtest.Factory")

			tf.RemotesStub = func() (glrepo.Remotes, error) {
				return nil, resolverErr
			}

			return NewCmdCreate(f)
		},
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	cli := "--source-branch test-branch --target-branch main --title Test --description TestDesc --no-editor --yes"
	_, err := exec(cli)

	require.Error(t, err, "actionable resolver errors must not be swallowed")
	assert.Contains(t, err.Error(), "GITLAB_HOST")
}

func TestMRCreate_SquashBeforeMergeFlag(t *testing.T) {
	tests := []struct {
		name                string
		flagValue           string
		flagSet             bool
		expectedSquashValue *bool
	}{
		{
			name:                "flag not set",
			flagSet:             false,
			expectedSquashValue: nil,
		},
		{
			name:                "flag set to true",
			flagValue:           "true",
			flagSet:             true,
			expectedSquashValue: new(true),
		},
		{
			name:                "flag set to false",
			flagValue:           "false",
			flagSet:             true,
			expectedSquashValue: new(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a command to test flag parsing
			cmd := NewCmdCreate(&cmdtest.Factory{})

			// Set up the command line args
			args := []string{"--title", "Test", "--description", "Desc"}
			if tt.flagSet {
				args = append(args, "--squash-before-merge="+tt.flagValue)
			}

			cmd.SetArgs(args)

			// Parse flags
			err := cmd.ParseFlags(args)
			require.NoError(t, err)

			// Get the options from the command
			opts := &options{}

			// Simulate what complete() does
			if cmd.Flags().Changed("squash-before-merge") {
				squash, _ := cmd.Flags().GetBool("squash-before-merge")
				opts.SquashBeforeMerge = &squash
			}

			// Verify the pointer is set correctly
			if tt.expectedSquashValue == nil {
				assert.Nil(t, opts.SquashBeforeMerge, "SquashBeforeMerge should be nil when flag is not set")
			} else {
				require.NotNil(t, opts.SquashBeforeMerge, "SquashBeforeMerge should not be nil when flag is set")
				assert.Equal(t, *tt.expectedSquashValue, *opts.SquashBeforeMerge, "SquashBeforeMerge value should match expected")
			}

			// Test what would be sent to the API
			mrCreateOpts := &gitlab.CreateMergeRequestOptions{}
			if opts.SquashBeforeMerge != nil {
				mrCreateOpts.Squash = opts.SquashBeforeMerge
			}

			// Verify API options
			if tt.expectedSquashValue == nil {
				assert.Nil(t, mrCreateOpts.Squash, "Squash should be nil when flag is not set")
			} else {
				require.NotNil(t, mrCreateOpts.Squash, "Squash should be set when flag is provided")
				assert.Equal(t, *tt.expectedSquashValue, *mrCreateOpts.Squash, "Squash API value should match flag value")
			}
		})
	}
}

func TestMRCreate_BooleanFlags(t *testing.T) {
	tests := []struct {
		name          string
		flagName      string
		flagValue     string
		flagSet       bool
		expectedValue *bool
		getOptValue   func(*options) *bool
		getAPIValue   func(*gitlab.CreateMergeRequestOptions) *bool
	}{
		// RemoveSourceBranch tests
		{
			name:          "remove-source-branch not set",
			flagName:      "remove-source-branch",
			flagSet:       false,
			expectedValue: nil,
			getOptValue:   func(o *options) *bool { return o.RemoveSourceBranch },
			getAPIValue:   func(opts *gitlab.CreateMergeRequestOptions) *bool { return opts.RemoveSourceBranch },
		},
		{
			name:          "remove-source-branch set to true",
			flagName:      "remove-source-branch",
			flagValue:     "true",
			flagSet:       true,
			expectedValue: new(true),
			getOptValue:   func(o *options) *bool { return o.RemoveSourceBranch },
			getAPIValue:   func(opts *gitlab.CreateMergeRequestOptions) *bool { return opts.RemoveSourceBranch },
		},
		{
			name:          "remove-source-branch set to false",
			flagName:      "remove-source-branch",
			flagValue:     "false",
			flagSet:       true,
			expectedValue: new(false),
			getOptValue:   func(o *options) *bool { return o.RemoveSourceBranch },
			getAPIValue:   func(opts *gitlab.CreateMergeRequestOptions) *bool { return opts.RemoveSourceBranch },
		},
		// AllowCollaboration tests
		{
			name:          "allow-collaboration not set",
			flagName:      "allow-collaboration",
			flagSet:       false,
			expectedValue: nil,
			getOptValue:   func(o *options) *bool { return o.AllowCollaboration },
			getAPIValue:   func(opts *gitlab.CreateMergeRequestOptions) *bool { return opts.AllowCollaboration },
		},
		{
			name:          "allow-collaboration set to true",
			flagName:      "allow-collaboration",
			flagValue:     "true",
			flagSet:       true,
			expectedValue: new(true),
			getOptValue:   func(o *options) *bool { return o.AllowCollaboration },
			getAPIValue:   func(opts *gitlab.CreateMergeRequestOptions) *bool { return opts.AllowCollaboration },
		},
		{
			name:          "allow-collaboration set to false",
			flagName:      "allow-collaboration",
			flagValue:     "false",
			flagSet:       true,
			expectedValue: new(false),
			getOptValue:   func(o *options) *bool { return o.AllowCollaboration },
			getAPIValue:   func(opts *gitlab.CreateMergeRequestOptions) *bool { return opts.AllowCollaboration },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a command to test flag parsing
			cmd := NewCmdCreate(&cmdtest.Factory{})

			// Set up the command line args
			args := []string{"--title", "Test", "--description", "Desc"}
			if tt.flagSet {
				args = append(args, "--"+tt.flagName+"="+tt.flagValue)
			}

			cmd.SetArgs(args)

			// Parse flags
			err := cmd.ParseFlags(args)
			require.NoError(t, err)

			// Get the options from the command
			opts := &options{}

			// Simulate what complete() does
			if cmd.Flags().Changed(tt.flagName) {
				value, _ := cmd.Flags().GetBool(tt.flagName)
				switch tt.flagName {
				case "remove-source-branch":
					opts.RemoveSourceBranch = &value
				case "allow-collaboration":
					opts.AllowCollaboration = &value
				}
			}

			// Verify the pointer is set correctly
			optValue := tt.getOptValue(opts)
			if tt.expectedValue == nil {
				assert.Nil(t, optValue, "%s should be nil when flag is not set", tt.flagName)
			} else {
				require.NotNil(t, optValue, "%s should not be nil when flag is set", tt.flagName)
				assert.Equal(t, *tt.expectedValue, *optValue, "%s value should match expected", tt.flagName)
			}

			// Test what would be sent to the API
			mrCreateOpts := &gitlab.CreateMergeRequestOptions{}
			if opts.RemoveSourceBranch != nil {
				mrCreateOpts.RemoveSourceBranch = opts.RemoveSourceBranch
			}
			if opts.AllowCollaboration != nil {
				mrCreateOpts.AllowCollaboration = opts.AllowCollaboration
			}

			// Verify API options
			apiValue := tt.getAPIValue(mrCreateOpts)
			if tt.expectedValue == nil {
				assert.Nil(t, apiValue, "%s should be nil in API when flag is not set", tt.flagName)
			} else {
				require.NotNil(t, apiValue, "%s should be set in API when flag is provided", tt.flagName)
				assert.Equal(t, *tt.expectedValue, *apiValue, "%s API value should match flag value", tt.flagName)
			}
		})
	}
}

func TestNewCmdCreate_WithAutoMerge(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListProjectTargetBranchRules (no rules configured)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	// Mock CreateMergeRequest
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:           1,
				IID:          12,
				ProjectID:    3,
				Title:        "myMRtitle",
				Description:  "myMRbody",
				State:        "opened",
				TargetBranch: "master",
				SourceBranch: "feat-new-mr",
				WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				SHA:          "abc123",
			},
		}, nil, nil)

	// Mock AcceptMergeRequest for auto-merge
	testClient.MockMergeRequests.EXPECT().
		AcceptMergeRequest("OWNER/REPO", int64(12), gomock.Any()).
		DoAndReturn(func(pid any, mr int64, opts *gitlab.AcceptMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			// Verify that AutoMerge is set to true and SHA is provided
			assert.NotNil(t, opts.AutoMerge)
			assert.True(t, *opts.AutoMerge)
			assert.NotNil(t, opts.SHA)
			assert.Equal(t, "abc123", *opts.SHA)

			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        "myMRtitle",
					Description:  "myMRbody",
					State:        "opened",
					TargetBranch: "master",
					SourceBranch: "feat-new-mr",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"-t", "myMRtitle",
		"-d", "myMRbody",
		"--auto-merge",
	}

	cli := strings.Join(cliStr, " ")

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}

	outputLines := strings.Split(output.String(), "\n")
	assert.Contains(t, outputLines[0], "!12 myMRtitle (feat-new-mr)")
	assert.Contains(t, output.Stderr(), "\nCreating merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
	assert.Contains(t, output.String(), "Auto-merge enabled. Will merge when all checks pass.")
}

func TestNewCmdCreate_WithAutoMergeFailure(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListProjectTargetBranchRules (no rules configured)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	// Mock CreateMergeRequest
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:           1,
				IID:          12,
				ProjectID:    3,
				Title:        "myMRtitle",
				Description:  "myMRbody",
				State:        "opened",
				TargetBranch: "master",
				SourceBranch: "feat-new-mr",
				WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				SHA:          "abc123",
			},
		}, nil, nil)

	// Mock AcceptMergeRequest to fail
	testClient.MockMergeRequests.EXPECT().
		AcceptMergeRequest("OWNER/REPO", int64(12), gomock.Any()).
		Return(nil, nil, errors.New("405 Method Not Allowed"))

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"-t", "myMRtitle",
		"-d", "myMRbody",
		"--auto-merge",
	}

	cli := strings.Join(cliStr, " ")

	output, err := exec(cli)

	// Should get an error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "merge request created but auto-merge could not be enabled")
	assert.Contains(t, err.Error(), "405 Method Not Allowed")

	// But the MR should still be displayed
	assert.Contains(t, output.String(), "!12 myMRtitle (feat-new-mr)")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestNewCmdCreate_TargetBranchRule(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "main",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListProjectTargetBranchRules: "feature/*" → "development"
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{
			{Name: "feature/*", TargetBranch: "development"},
		}, nil, nil)

	// Mock CreateMergeRequest: verify target branch is "development", not "main"
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "development", *opts.TargetBranch)
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        "myMRtitle",
					Description:  "myMRbody",
					State:        "opened",
					TargetBranch: "development",
					SourceBranch: "feature/my-feature",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: main\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feature/my-feature
		deadbeef refs/remotes/origin/feature/my-feature
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feature/my-feature", nil
			}
		},
	)

	output, err := exec("-t myMRtitle -d myMRbody")
	require.NoError(t, err)
	assert.Contains(t, output.Stderr(), "Creating merge request for feature/my-feature into development in OWNER/REPO")
}

func TestNewCmdCreate_WithTemplate(t *testing.T) {
	// Cannot use t.Parallel(): test mutates git.ToplevelDir, a package-level variable.
	t.Setenv("NO_COLOR", "true")

	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, ".gitlab", "merge_request_templates")
	require.NoError(t, os.MkdirAll(templateDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "bug_fix.md"), []byte("## What does this MR do?"), 0o644))

	origToplevelDir := git.ToplevelDir
	git.ToplevelDir = func() (string, error) { return tmpDir, nil }
	t.Cleanup(func() { git.ToplevelDir = origToplevelDir })

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "## What does this MR do?", *opts.Description)
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:          12,
					Title:        "Fix the bug",
					SourceBranch: "feat-new-mr",
					TargetBranch: "master",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{Name: "upstream", Resolved: "head", PushURL: pu},
						Repo:   glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{Name: "origin", Resolved: "base", PushURL: pu},
						Repo:   glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) { return "feat-new-mr", nil }
		},
	)

	output, err := exec(`--title "Fix the bug" --template bug_fix --source-branch feat-new-mr --yes`)
	require.NoError(t, err)
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestNewCmdCreate_WithTemplateMdExtension(t *testing.T) {
	// Cannot use t.Parallel(): test mutates git.ToplevelDir, a package-level variable.
	t.Setenv("NO_COLOR", "true")

	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, ".gitlab", "merge_request_templates")
	require.NoError(t, os.MkdirAll(templateDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "bug_fix.md"), []byte("## What does this MR do?"), 0o644))

	origToplevelDir := git.ToplevelDir
	git.ToplevelDir = func() (string, error) { return tmpDir, nil }
	t.Cleanup(func() { git.ToplevelDir = origToplevelDir })

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "## What does this MR do?", *opts.Description)
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:          12,
					Title:        "Fix the bug",
					SourceBranch: "feat-new-mr",
					TargetBranch: "master",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{Name: "upstream", Resolved: "head", PushURL: pu},
						Repo:   glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{Name: "origin", Resolved: "base", PushURL: pu},
						Repo:   glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) { return "feat-new-mr", nil }
		},
	)

	// Passing "bug_fix.md" should work the same as "bug_fix"
	output, err := exec(`--title "Fix the bug" --template bug_fix.md --source-branch feat-new-mr --yes`)
	require.NoError(t, err)
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestNewCmdCreate_TemplateNotFound(t *testing.T) {
	// Cannot use t.Parallel(): test mutates git.ToplevelDir, a package-level variable.
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".gitlab", "merge_request_templates"), 0o755))

	origToplevelDir := git.ToplevelDir
	git.ToplevelDir = func() (string, error) { return tmpDir, nil }
	t.Cleanup(func() { git.ToplevelDir = origToplevelDir })

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{Name: "upstream", Resolved: "head", PushURL: pu},
						Repo:   glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{Name: "origin", Resolved: "base", PushURL: pu},
						Repo:   glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) { return "feat-new-mr", nil }
		},
	)

	_, err := exec(`--title "Fix the bug" --template nonexistent --source-branch feat-new-mr --yes`)
	assert.ErrorContains(t, err, `template "nonexistent" not found in .gitlab/merge_request_templates/`)
}

func TestNewCmdCreate_TemplateMutuallyExclusiveWithDescription(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false)

	_, err := exec(`--title "Fix" --template bug_fix --description "my description" --source-branch feat-new-mr`)
	assert.Error(t, err)
}

func TestNewCmdCreate_TemplateMutuallyExclusiveWithFill(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false)

	_, err := exec(`--title "Fix" --template bug_fix --fill --source-branch feat-new-mr`)
	assert.Error(t, err)
}

func TestMatchBranchPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		branch  string
		want    bool
	}{
		{"*", "main", true},
		{"*", "feature/foo", true},
		{"feature/*", "feature/my-feature", true},
		{"feature/*", "feature/foo/bar", true},
		{"feature/*", "hotfix/oops", false},
		{"hotfix/*", "hotfix/urgent", true},
		{"main", "main", true},
		{"main", "maintenance", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"/"+tt.branch, func(t *testing.T) {
			t.Parallel()
			got, err := matchBranchPattern(tt.pattern, tt.branch)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewCmdCreate_DescriptionFile(t *testing.T) {
	const body = "# Heading\n\nMulti-line body with \"quotes\", $VARS and `backticks`.\n"

	tests := []struct {
		name     string
		useStdin bool
	}{
		{name: "reads the description from a file"},
		{name: "reads the description from standard input", useStdin: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "true")

			testClient := gitlabtesting.NewTestClient(t)
			testClient.MockProjects.EXPECT().
				GetProject("OWNER/REPO", gomock.Any()).
				Return(&gitlab.Project{
					ID:                   1,
					DefaultBranch:        "master",
					WebURL:               "http://gitlab.com/OWNER/REPO",
					MergeRequestsEnabled: true,
					PathWithNamespace:    "OWNER/REPO",
				}, nil, nil)
			testClient.MockProjects.EXPECT().
				ListProjectTargetBranchRules("OWNER/REPO", gomock.Any()).
				Return([]gitlab.TargetBranchRule{}, nil, nil)

			var gotDescription *string
			testClient.MockMergeRequests.EXPECT().
				CreateMergeRequest("OWNER/REPO", gomock.Any()).
				DoAndReturn(func(_ any, opts *gitlab.CreateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
					gotDescription = opts.Description
					return &gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							IID:          12,
							Title:        "myMRtitle",
							State:        "opened",
							TargetBranch: "master",
							SourceBranch: "feat-new-mr",
							WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
						},
					}, nil, nil
				})

			cs, csTeardown := test.InitCmdStubber()
			defer csTeardown()
			cs.Stub("HEAD branch: master\n")
			cs.Stub(heredoc.Doc(`
				deadbeef HEAD
				deadb00f refs/remotes/upstream/feat-new-mr
				deadbeef refs/remotes/origin/feat-new-mr
			`))

			pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

			factoryOpts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(testClient.Client),
				func(f *cmdtest.Factory) {
					f.RemotesStub = func() (glrepo.Remotes, error) {
						return glrepo.Remotes{
							{
								Remote: &git.Remote{Name: "upstream", Resolved: "head", PushURL: pu},
								Repo:   glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
							},
							{
								Remote: &git.Remote{Name: "origin", Resolved: "base", PushURL: pu},
								Repo:   glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
							},
						}, nil
					}
					f.BranchStub = func() (string, error) { return "feat-new-mr", nil }
				},
			}

			path := "-"
			if tt.useStdin {
				factoryOpts = append(factoryOpts, cmdtest.WithStdin(body))
			} else {
				path = filepath.Join(t.TempDir(), "description.md")
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, factoryOpts...)

			_, err := exec(fmt.Sprintf("-t myMRtitle --description-file %q --yes", path))
			require.NoError(t, err)

			require.NotNil(t, gotDescription)
			assert.Equal(t, body, *gotDescription)
		})
	}
}

func TestNewCmdCreate_DescriptionFileConflicts(t *testing.T) {
	tests := []struct {
		name string
		cli  string
		want string
	}{
		{
			name: "with --description",
			cli:  `-t title --description inline --description-file some.md`,
			want: "[description description-file]",
		},
		{
			name: "with --template",
			cli:  `-t title --template bug_fix --description-file some.md`,
			want: "[template description-file]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false)

			_, err := exec(tt.cli)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
