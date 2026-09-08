//go:build !integration

package checkout

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	git_testing "gitlab.com/gitlab-org/cli/internal/git/testing"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func setupTest(t *testing.T, testClient *gitlabtesting.TestClient, opts ...cmdtest.FactoryOption) func(string) (*test.CmdOut, error) {
	t.Helper()

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	defaultOpts := []cmdtest.FactoryOption{
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBranch("main"),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "base",
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
		},
	}

	return cmdtest.SetupCmdForTest(t, NewCmdCheckout, false, append(defaultOpts, opts...)...)
}

func TestMrCheckout(t *testing.T) {
	t.Parallel()
	t.Run("when a valid MR is checked out using MR id", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:                 123,
					IID:                123,
					ProjectID:          3,
					SourceProjectID:    3,
					SourceBranch:       "feat-new-mr",
					Title:              "test mr title",
					Description:        "test mr description",
					AllowCollaboration: false,
					State:              "opened",
					SHA:                "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "Counting objects")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when a valid MR comes from a forked private project", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:                 123,
					IID:                123,
					ProjectID:          3,
					SourceProjectID:    3,
					TargetProjectID:    4,
					SourceBranch:       "feat-new-mr",
					Title:              "test mr title",
					Description:        "test mr description",
					AllowCollaboration: false,
					State:              "opened",
					SHA:                "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(nil, nil, &gitlab.ErrorResponse{Message: "404 Project Not Found"})

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           4,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/merge-requests/123/head:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/merge-requests/123/head:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/merge-requests/123/head").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/merge-requests/123/head:feat-new-mr")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when a valid MR is checked out using MR id and specifying branch", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:                 123,
					IID:                123,
					ProjectID:          3,
					SourceProjectID:    4,
					SourceBranch:       "feat-new-mr",
					Title:              "test mr title",
					Description:        "test mr description",
					AllowCollaboration: true,
					State:              "opened",
					SHA:                "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:FORK_OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/foo").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:FORK_OWNER/REPO.git", "refs/heads/feat-new-mr:foo").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:foo"))
		mockGit.EXPECT().Git("config", "branch.foo.remote", "git@gitlab.com:FORK_OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.foo.pushRemote", "git@gitlab.com:FORK_OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.foo.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "foo").
			DoAndReturn(git.CheckoutStub("foo"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123 --branch foo")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:foo")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'foo'")
	})

	t.Run("when initial fetch fails but retry succeeds", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "couldn't find remote ref"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found")).Times(2)
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "fetch attempt refs/heads/feat-new-mr:feat-new-mr failed")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when fetch fails completely", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "fetch failed"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr", "fetch failed"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch failed")
		assert.Contains(t, output.Stderr(), "fetch attempt refs/heads/feat-new-mr:feat-new-mr failed")
		assert.Contains(t, output.Stderr(), "fetch attempt refs/heads/feat-new-mr failed")
		assert.Empty(t, output.String())
	})

	t.Run("when checkout fails", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.FailingCheckoutStub("feat-new-mr", "pathspec 'feat-new-mr' did not match"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not checkout branch")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.Contains(t, output.Stderr(), "error: pathspec 'feat-new-mr' did not match")
		assert.Empty(t, output.String())
	})

	t.Run("when git config fails", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").
			Return("", errors.New("could not set config"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not set config")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.Empty(t, output.String())
	})

	t.Run("when diverged without --force and non-interactive", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("main\n", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "--force")
		var fe cmdutils.FlagError
		require.ErrorAs(t, err, &fe)
	})

	t.Run("when --force, on target, clean", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("", nil)
		mockGit.EXPECT().Git("ls-files", "--others", "--exclude-standard").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "reset", "--hard", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.NoError(t, err)
	})

	t.Run("when --force, on target, tracked modifications refuse", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("file.go\n", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "changes that would be lost")
	})

	t.Run("when --force, on target, untracked file conflicts with incoming", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("", nil)
		mockGit.EXPECT().Git("ls-files", "--others", "--exclude-standard").Return("local.txt\n", nil)
		mockGit.EXPECT().Git("ls-tree", "-r", "--name-only", "FETCH_HEAD").Return("main.txt\nlocal.txt\n", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "changes that would be lost")
	})

	t.Run("when --force, on target, unrelated untracked file allowed", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("", nil)
		mockGit.EXPECT().Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\n", nil)
		mockGit.EXPECT().Git("ls-tree", "-r", "--name-only", "FETCH_HEAD").Return("main.txt\nfeature.go\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "reset", "--hard", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.NoError(t, err)
	})

	t.Run("when --force, not on target", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("main\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "branch", "-f", "feat-new-mr", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.NoError(t, err)
	})

	t.Run("when local branch already matches MR head, fetch is skipped", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("abc123\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", gomock.Any(), gomock.Any()).Times(0)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.String(), "already at merge request head")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when local branch exists but at a different SHA, fetch still happens", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("stale000\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.NotContains(t, output.String(), "skipping fetch")
	})

	t.Run("when commit is already local but branch is missing, branch is created and fetch is skipped", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("abc123\n", nil)
		mockGit.EXPECT().Git("branch", "feat-new-mr", "abc123").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", gomock.Any(), gomock.Any()).Times(0)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.String(), "Created branch")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when branch and commit are both missing, fetch happens", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.NotContains(t, output.String(), "skipping fetch")
	})

	t.Run("when merge request has no head SHA", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           123,
					IID:          123,
					SourceBranch: "feat-new-mr",
					State:        "opened",
				},
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no head commit")
	})
}

func newDivergenceTestClient(t *testing.T) *gitlabtesting.TestClient {
	t.Helper()
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				State:           "opened",
				SHA:             "abc123",
			},
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:           3,
			SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
		}, nil, nil)
	return testClient
}

func TestUnsafeToReset(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	cases := []struct {
		name        string
		mocks       func(m *git_testing.MockGitRunnerMockRecorder)
		wantBlocked bool
		wantErr     error
	}{
		{
			name: "clean tree",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("", nil)
			},
		},
		{
			name: "tracked diff single file",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("file.go\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "tracked diff multiple files",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("a.go\nb.go\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "unrelated untracked only",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "FETCH_HEAD").Return("main.txt\nfeature.go\n", nil)
			},
		},
		{
			name: "conflicting untracked",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("conflict.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "FETCH_HEAD").Return("main.txt\nconflict.txt\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "multiple untracked one conflicts",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\nconflict.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "FETCH_HEAD").Return("main.txt\nconflict.txt\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "trailing blank lines parsed",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\n\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "FETCH_HEAD").Return("main.txt\n\n", nil)
			},
		},
		{
			name: "diff errors propagate",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", boom)
			},
			wantErr: boom,
		},
		{
			name: "ls-files errors propagate",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("", boom)
			},
			wantErr: boom,
		},
		{
			name: "ls-tree errors propagate",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("foo.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "FETCH_HEAD").Return("", boom)
			},
			wantErr: boom,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockGit := git_testing.NewMockGitRunner(ctrl)
			tc.mocks(mockGit.EXPECT())

			o := &options{gr: mockGit}
			got, err := o.unsafeToReset()

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantBlocked, got)
		})
	}
}

func TestMrCheckout_SetUpstreamTo(t *testing.T) {
	t.Parallel()
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				State:           "opened",
				SHA:             "abc123",
			},
		}, nil, nil)

	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:           3,
			SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
		}, nil, nil)

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
	mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
		DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
		DoAndReturn(git.CheckoutStub("feat-new-mr"))
	mockGit.EXPECT().Git("branch", "--set-upstream-to", "upstream/main").Return("", nil)

	exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
	output, err := exec("123 --set-upstream-to upstream/main")

	require.NoError(t, err)
	assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
	assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
}

func TestMrCheckout_HTTPSProtocolConfiguration(t *testing.T) {
	t.Parallel()
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:                 123,
				IID:                123,
				ProjectID:          3,
				SourceProjectID:    3,
				SourceBranch:       "feat-new-mr",
				Title:              "test mr title",
				Description:        "test mr description",
				AllowCollaboration: false,
				State:              "opened",
				SHA:                "abc123",
			},
		}, nil, nil)

	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:            3,
			HTTPURLToRepo: "https://gitlab.com/OWNER/REPO.git",
			SSHURLToRepo:  "git@gitlab.com:OWNER/REPO.git",
		}, nil, nil)

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
	mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "https://gitlab.com/OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
		DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "https://gitlab.com/OWNER/REPO.git").Return("", nil)
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
		DoAndReturn(git.CheckoutStub("feat-new-mr"))

	cfg := config.NewBlankConfig()
	err := cfg.Set("gitlab.com", "git_protocol", "https")
	require.NoError(t, err)

	exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit), cmdtest.WithConfig(cfg))
	output, err := exec("123")

	require.NoError(t, err)
	assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
	assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
}

// TestMrCheckout_CrossHostURLUsesURLHostProtocol verifies that when an MR URL pointing at a
// different host is given, the git_protocol lookup uses the URL's host (not the local repo's
// host). Before the refactor, baseRepo came from f.BaseRepo(); now it comes from MRFromArgs,
// which overrides baseRepo with the URL repo for cross-host URLs.
func TestMrCheckout_CrossHostURLUsesURLHostProtocol(t *testing.T) {
	t.Parallel()
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				Title:           "test mr title",
				Description:     "test mr description",
				State:           "opened",
				SHA:             "abc123",
			},
		}, nil, nil)

	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:            3,
			HTTPURLToRepo: "https://custom.host.com/OWNER/REPO.git",
			SSHURLToRepo:  "git@custom.host.com:OWNER/REPO.git",
		}, nil, nil)

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
	mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
	// The fetch URL must use HTTPS (from custom.host.com's config), not SSH (from gitlab.com's config).
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "https://custom.host.com/OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
		DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "https://custom.host.com/OWNER/REPO.git").Return("", nil)
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
		DoAndReturn(git.CheckoutStub("feat-new-mr"))

	cfg := config.NewBlankConfig()
	err := cfg.Set("custom.host.com", "git_protocol", "https")
	require.NoError(t, err)
	// gitlab.com intentionally left with default (ssh) to distinguish the two hosts.

	exec := setupTest(t, testClient,
		cmdtest.WithGitRunner(mockGit),
		cmdtest.WithConfig(cfg),
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)
	output, err := exec("https://custom.host.com/OWNER/REPO/-/merge_requests/123")

	require.NoError(t, err)
	assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
	assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
}
