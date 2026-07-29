//go:build !integration

package sync

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	git_testing "gitlab.com/gitlab-org/cli/internal/git/testing"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type SyncScenario struct {
	refs           map[string]TestRef
	title          string
	baseBranch     string
	pushNeeded     bool
	pushErr        error
	noVerify       bool
	updateBase     bool
	rebaseError    bool
	skipMRCreation bool
	assignees      []string
	labels         []string
	reviewers      []string
}

type TestRef struct {
	ref   git.StackRef
	state string
}

func setupTestFactory(t *testing.T, testClient *gitlabtesting.TestClient) (cmdutils.Factory, *options) {
	t.Helper()

	ios, _, _, _ := cmdtest.TestIOStreams()

	// Create api.Client that wraps the mock gitlab.Client
	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: ""}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	f := cmdtest.NewTestFactory(ios,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.BaseRepoStub = func() (glrepo.Interface, error) {
				return glrepo.TestProject("stack_guy", "stackproject"), nil
			}
			f.ApiClientStub = func(repoHost string) (*api.Client, error) {
				return apiClient, nil
			}
		},
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				r := glrepo.Remotes{
					&glrepo.Remote{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "head: gitlab.com/stack_guy/stackproject",
						},
						Repo: glrepo.TestProject("stack_guy", "stackproject"),
					},
				}
				return r, nil
			}
		},
	)

	client, _ := f.GitLabClient()

	return f, &options{
		io:        ios,
		remotes:   f.Remotes,
		labClient: client,
		baseRepo:  f.BaseRepo,
	}
}

func TestNewCmdSyncStack_Flags(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)
	var gr git.StandardGitCommand

	cmd := NewCmdSyncStack(f, gr)

	// Test --no-verify flag exists
	noVerifyFlag := cmd.Flag("no-verify")
	require.NotNil(t, noVerifyFlag)
	assert.Equal(t, "false", noVerifyFlag.DefValue)
	assert.Contains(t, noVerifyFlag.Usage, "pre-push hook")

	// Test --update-base flag exists
	updateBaseFlag := cmd.Flag("update-base")
	require.NotNil(t, updateBaseFlag)
	assert.Equal(t, "false", updateBaseFlag.DefValue)
	assert.Contains(t, updateBaseFlag.Usage, "base branch")

	// Test --skip-mr-creation flag exists
	skipMRCreationFlag := cmd.Flag("skip-mr-creation")
	require.NotNil(t, skipMRCreationFlag)
	assert.Equal(t, "false", skipMRCreationFlag.DefValue)
	assert.Contains(t, skipMRCreationFlag.Usage, "merge requests")

	// Test --assignee flag exists
	assigneeFlag := cmd.Flag("assignee")
	require.NotNil(t, assigneeFlag)
	assert.Equal(t, "[]", assigneeFlag.DefValue)
	assert.Equal(t, "a", assigneeFlag.Shorthand)
	assert.Contains(t, assigneeFlag.Usage, "usernames")

	// Test --label flag exists
	labelFlag := cmd.Flag("label")
	require.NotNil(t, labelFlag)
	assert.Equal(t, "[]", labelFlag.DefValue)
	assert.Equal(t, "l", labelFlag.Shorthand)
	assert.Contains(t, labelFlag.Usage, "name")

	// Test --reviewer flag exists
	reviewerFlag := cmd.Flag("reviewer")
	require.NotNil(t, reviewerFlag)
	assert.Equal(t, "[]", reviewerFlag.DefValue)
	assert.Contains(t, reviewerFlag.Usage, "usernames")
}

func Test_stackSync(t *testing.T) {
	type args struct {
		stack SyncScenario
	}

	forcePushErr := errors.New("pre-push hook rejected force push")

	tests := []struct {
		name       string
		args       args
		setupMocks func(t *testing.T, testClient *gitlabtesting.TestClient)
		wantErr    bool
		wantErrMsg string
		wantErrIs  error
	}{
		{
			name: "two branches, 1st branch has MR, 2nd branch behind, stacks are named",
			args: args{
				stack: SyncScenario{
					title: "my cool stack",
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR:          "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
								Description: "single line desc",
							},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: "", Description: "multi line desc\n\ndescription, bark!"},
							state: BranchIsBehind,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockListStackMRsByBranch("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								Title:        "test mr title",
								TargetBranch: "main",
								SourceBranch: "Branch1",
								State:        "opened",
								Description:  "test mr description25",
								Author:       &gitlab.BasicUser{ID: 1, Username: "admin"},
							},
						}, nil, nil
					})

				// MockGetStackMR("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:           25,
							IID:          25,
							ProjectID:    3,
							Title:        "test mr title",
							TargetBranch: "main",
							SourceBranch: "Branch1",
							State:        "opened",
							Description:  "test mr description25",
							Author:       &gitlab.BasicUser{ID: 1, Username: "admin"},
						},
					}, nil, nil)

				// MockPostStackMR(Source: "Branch2", Target: "Branch1", Title: "multi line desc", Description: "description, bark!")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						assert.Equal(t, "multi line desc", *opts.Title)
						assert.Equal(t, "description, bark!", *opts.Description)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          42,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
								Title:        "multi line desc",
								Description:  "description, bark!",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "two branches, no MRs, nothing to commit",
			args: args{
				stack: SyncScenario{
					title: "my cool stack",
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "2", Branch: "Branch1", MR: "", Description: "some description"},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: ""},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockPostStackMR(Source: "Branch1", Target: "main", Title: "some description")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "main", *opts.TargetBranch)
						assert.Equal(t, "some description", *opts.Title)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          43,
								SourceBranch: "Branch1",
								TargetBranch: "main",
								Title:        "some description",
							},
						}, nil, nil
					})

				// MockPostStackMR(Source: "Branch2", Target: "Branch1")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          44,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "a complicated scenario",
			args: args{
				stack: SyncScenario{
					title:      "my cool stack",
					pushNeeded: true,
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
							},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "3", Branch: "Branch2", MR: ""},
							state: NothingToCommit,
						},
						"3": {
							ref:   git.StackRef{SHA: "3", Prev: "2", Next: "4", Branch: "Branch3", MR: ""},
							state: NothingToCommit,
						},
						"4": {
							ref:   git.StackRef{SHA: "4", Prev: "3", Next: "5", Branch: "Branch4", MR: ""},
							state: BranchHasDiverged,
						},
						"5": {
							ref:   git.StackRef{SHA: "5", Prev: "4", Next: "6", Branch: "Branch5", MR: ""},
							state: NothingToCommit,
						},
						"6": {
							ref:   git.StackRef{SHA: "6", Prev: "5", Next: "", Branch: "Branch6", MR: ""},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockListStackMRsByBranch("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								SourceBranch: "Branch1",
								State:        "opened",
							},
						}, nil, nil
					})

				// MockGetStackMR("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:           25,
							IID:          25,
							ProjectID:    3,
							SourceBranch: "Branch1",
							State:        "opened",
						},
					}, nil, nil)

				// Create MRs for Branch2-6
				for i := 2; i <= 6; i++ {
					testClient.MockMergeRequests.EXPECT().
						CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
						Return(&gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID: int64(40 + i),
							},
						}, nil, nil)
				}
			},
		},
		{
			name: "non standard base branch",
			args: args{
				stack: SyncScenario{
					title:      "my cool stack",
					baseBranch: "jawn",
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "2", Branch: "Branch1", MR: ""},
							state: BranchIsBehind,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: ""},
							state: BranchIsBehind,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockPostStackMR(Source: "Branch1", Target: "jawn")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "jawn", *opts.TargetBranch)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          45,
								SourceBranch: "Branch1",
								TargetBranch: "jawn",
							},
						}, nil, nil
					})

				// MockPostStackMR(Source: "Branch2", Target: "Branch1")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          46,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "no-verify flag is passed to git push",
			args: args{
				stack: SyncScenario{
					title:    "my cool stack",
					noVerify: true,
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "2", Branch: "Branch1", MR: "", Description: "first branch"},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: "", Description: "second branch"},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockPostStackMR(Source: "Branch1", Target: "main", Title: "first branch")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "main", *opts.TargetBranch)
						assert.Equal(t, "first branch", *opts.Title)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          47,
								SourceBranch: "Branch1",
								TargetBranch: "main",
								Title:        "first branch",
							},
						}, nil, nil
					})

				// MockPostStackMR(Source: "Branch2", Target: "Branch1", Title: "second branch")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						assert.Equal(t, "second branch", *opts.Title)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          48,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
								Title:        "second branch",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "no-verify flag with force push",
			args: args{
				stack: SyncScenario{
					title:      "my cool stack",
					noVerify:   true,
					pushNeeded: true,
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
							},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: ""},
							state: BranchHasDiverged,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockListStackMRsByBranch("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								Title:        "test mr title",
								TargetBranch: "main",
								SourceBranch: "Branch1",
								State:        "opened",
								Description:  "test mr description25",
								Author:       &gitlab.BasicUser{ID: 1, Username: "admin"},
							},
						}, nil, nil
					})

				// MockGetStackMR("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:           25,
							IID:          25,
							ProjectID:    3,
							Title:        "test mr title",
							TargetBranch: "main",
							SourceBranch: "Branch1",
							State:        "opened",
							Description:  "test mr description25",
							Author:       &gitlab.BasicUser{ID: 1, Username: "admin"},
						},
					}, nil, nil)

				// MockPostStackMR(Source: "Branch2", Target: "Branch1")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          49,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "update-base rebases stack onto base branch with diverged branches",
			args: args{
				stack: SyncScenario{
					title:      "my cool stack",
					updateBase: true,
					pushNeeded: true,
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
							},
							state: BranchHasDiverged,
						},
						"2": {
							ref: git.StackRef{
								SHA: "2", Prev: "1", Next: "", Branch: "Branch2",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/2",
							},
							state: BranchHasDiverged,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								SourceBranch: *opts.SourceBranch,
								State:        "opened",
							},
						}, nil, nil
					}).Times(2)

				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:        25,
							IID:       25,
							ProjectID: 3,
							State:     "opened",
						},
					}, nil, nil).Times(2)
			},
		},

		{
			name: "update-base with custom base branch",
			args: args{
				stack: SyncScenario{
					title:      "my cool stack",
					baseBranch: "develop",
					updateBase: true,
					pushNeeded: true,
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
							},
							state: BranchHasDiverged,
						},
						"2": {
							ref: git.StackRef{
								SHA: "2", Prev: "1", Next: "", Branch: "Branch2",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/2",
							},
							state: BranchHasDiverged,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								SourceBranch: *opts.SourceBranch,
								State:        "opened",
							},
						}, nil, nil
					}).Times(2)

				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:        25,
							IID:       25,
							ProjectID: 3,
							State:     "opened",
						},
					}, nil, nil).Times(2)
			},
		},

		{
			name: "failed force-push hook preserves wrapped error",
			args: args{
				stack: SyncScenario{
					title:      "force push failure",
					updateBase: true,
					pushNeeded: true,
					pushErr:    forcePushErr,
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA:    "1",
								Branch: "Branch1",
								MR:     "http://gitlab.com/stack_guy/stackproject/-/merge_requests/25",
							},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)
				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					Return([]*gitlab.BasicMergeRequest{
						{
							ID:           25,
							IID:          25,
							ProjectID:    3,
							SourceBranch: "Branch1",
							State:        "opened",
						},
					}, nil, nil)
				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:           25,
							IID:          25,
							ProjectID:    3,
							SourceBranch: "Branch1",
							State:        "opened",
						},
					}, nil, nil)
			},
			wantErr:    true,
			wantErrMsg: "error pushing branches to remote",
			wantErrIs:  forcePushErr,
		},

		{
			name: "update-base rebase failure includes target branch name in error",
			args: args{
				stack: SyncScenario{
					title:       "my cool stack",
					updateBase:  true,
					rebaseError: true,
					baseBranch:  "feature-branch",
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
							},
							state: NothingToCommit,
						},
						"2": {
							ref: git.StackRef{
								SHA: "2", Prev: "1", Next: "", Branch: "Branch2",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/2",
							},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)
			},
			wantErr:    true,
			wantErrMsg: "could not rebase onto origin/feature-branch",
		},

		{
			name: "single branch with custom assignees",
			args: args{
				stack: SyncScenario{
					title:     "assignee stack",
					assignees: []string{"reviewer1", "reviewer2"},
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "", Branch: "Branch1", MR: "", Description: "test MR"},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy", ID: 100}, nil, nil)

				testClient.MockUsers.EXPECT().
					ListUsers(gomock.Any()).
					Return([]*gitlab.User{{ID: 201, Username: "reviewer1"}}, nil, nil)
				testClient.MockUsers.EXPECT().
					ListUsers(gomock.Any()).
					Return([]*gitlab.User{{ID: 202, Username: "reviewer2"}}, nil, nil)

				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "main", *opts.TargetBranch)
						assert.Equal(t, "test MR", *opts.Title)
						assert.NotNil(t, opts.AssigneeIDs)
						assert.ElementsMatch(t, []int64{201, 202}, *opts.AssigneeIDs)
						assert.Nil(t, opts.AssigneeID)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          47,
								SourceBranch: "Branch1",
								TargetBranch: "main",
								Title:        "test MR",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "single branch with custom labels",
			args: args{
				stack: SyncScenario{
					title:  "label stack",
					labels: []string{"bug", "priority::high"},
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "", Branch: "Branch1", MR: "", Description: "test MR with labels"},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy", ID: 100}, nil, nil)

				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "main", *opts.TargetBranch)
						assert.Equal(t, "test MR with labels", *opts.Title)
						assert.NotNil(t, opts.Labels)
						assert.ElementsMatch(t, []string{"bug", "priority::high"}, *opts.Labels)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          47,
								SourceBranch: "Branch1",
								TargetBranch: "main",
								Title:        "test MR with labels",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "single branch with custom reviewers",
			args: args{
				stack: SyncScenario{
					title:     "reviewer stack",
					reviewers: []string{"reviewer1", "reviewer2"},
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "", Branch: "Branch1", MR: "", Description: "test MR with reviewers"},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy", ID: 100}, nil, nil)

				testClient.MockUsers.EXPECT().
					ListUsers(gomock.Any()).
					Return([]*gitlab.User{{ID: 201, Username: "reviewer1"}}, nil, nil)
				testClient.MockUsers.EXPECT().
					ListUsers(gomock.Any()).
					Return([]*gitlab.User{{ID: 202, Username: "reviewer2"}}, nil, nil)

				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "main", *opts.TargetBranch)
						assert.Equal(t, "test MR with reviewers", *opts.Title)
						assert.NotNil(t, opts.ReviewerIDs)
						assert.ElementsMatch(t, []int64{201, 202}, *opts.ReviewerIDs)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          47,
								SourceBranch: "Branch1",
								TargetBranch: "main",
								Title:        "test MR with reviewers",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "skip-mr-creation skips MR creation for branches without MRs",
			args: args{
				stack: SyncScenario{
					title:          "skip mr stack",
					skipMRCreation: true,
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "2", Branch: "Branch1", MR: "", Description: "first branch"},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: "", Description: "second branch"},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)
			},
		},

		{
			name: "skip-mr-creation still checks existing MRs",
			args: args{
				stack: SyncScenario{
					title:          "skip mr with existing",
					skipMRCreation: true,
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR:          "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
								Description: "has MR",
							},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: "", Description: "no MR"},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								SourceBranch: "Branch1",
								State:        "opened",
							},
						}, nil, nil
					})

				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:        25,
							IID:       25,
							ProjectID: 3,
							State:     "opened",
						},
					}, nil, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			git.InitGitRepoWithCommit(t)

			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMocks(t, testClient)

			ctrl := gomock.NewController(t)
			mockCmd := git_testing.NewMockGitRunner(ctrl)

			f, opts := setupTestFactory(t, testClient)

			// Set options from test case
			opts.noVerify = tc.args.stack.noVerify
			opts.updateBase = tc.args.stack.updateBase
			opts.skipMRCreation = tc.args.stack.skipMRCreation
			opts.assignees = tc.args.stack.assignees
			opts.labels = tc.args.stack.labels
			opts.reviewers = tc.args.stack.reviewers

			err := git.SetConfig("glab.currentstack", tc.args.stack.title)
			require.NoError(t, err)

			createStack(t, tc.args.stack.title, tc.args.stack.refs)
			stack, err := git.GatherStackRefs(tc.args.stack.title)
			require.NoError(t, err)

			mockCmd.EXPECT().Git([]string{"fetch", "origin"})

			if tc.args.stack.updateBase {
				baseBranch := "main"
				if tc.args.stack.baseBranch != "" {
					baseBranch = tc.args.stack.baseBranch
					err := git.AddStackBaseBranch(tc.args.stack.title, tc.args.stack.baseBranch)
					require.NoError(t, err)
				} else {
					mockCmd.EXPECT().Git([]string{"remote", "show", "origin"}).Return("HEAD branch: main", nil)
				}

				mockCmd.EXPECT().Git([]string{"checkout", stack.Last().Branch})
				if tc.args.stack.rebaseError {
					mockCmd.EXPECT().Git([]string{"rebase", "--fork-point", "--update-refs", "origin/" + baseBranch}).
						Return("", fmt.Errorf("conflict"))
				} else {
					mockCmd.EXPECT().Git([]string{"rebase", "--fork-point", "--update-refs", "origin/" + baseBranch})
				}
			}

			if !tc.args.stack.rebaseError {
				for ref := range stack.Iter() {
					state := tc.args.stack.refs[ref.SHA].state

					mockCmd.EXPECT().Git([]string{"checkout", ref.Branch})
					mockCmd.EXPECT().Git([]string{"status", "-uno"}).Return(state, nil)

					switch state {
					case BranchIsBehind:
						mockCmd.EXPECT().Git([]string{"pull"}).Return(state, nil)

					case BranchHasDiverged:
						mockCmd.EXPECT().Git([]string{"checkout", stack.Last().Branch})
						mockCmd.EXPECT().Git([]string{"rebase", "--fork-point", "--update-refs", ref.Branch})

					case NothingToCommit:
					}

					if ref.MR == "" && !tc.args.stack.skipMRCreation {
						if ref.IsFirst() == true {
							if tc.args.stack.baseBranch != "" {
								err := git.AddStackBaseBranch(tc.args.stack.title, tc.args.stack.baseBranch)
								require.NoError(t, err)
								mockCmd.EXPECT().Git([]string{"ls-remote", "--exit-code", "--heads", "origin", tc.args.stack.baseBranch})
							} else {
								// this is to check for the default branch
								mockCmd.EXPECT().Git([]string{"remote", "show", "origin"}).Return("HEAD branch: main", nil)
								mockCmd.EXPECT().Git([]string{"ls-remote", "--exit-code", "--heads", "origin", "main"})
							}
						}

						// Build push command with --no-verify if noVerify is set
						pushCmd := []string{"push", "--set-upstream", "origin"}
						if tc.args.stack.noVerify {
							pushCmd = append(pushCmd, "--no-verify")
						}
						pushCmd = append(pushCmd, ref.Branch)
						mockCmd.EXPECT().
							GitWithIO(gomock.Any(), gomock.Any(), pushCmd).
							Return(nil)

					}
				}
			}

			if tc.args.stack.pushNeeded {
				command := []string{"push", "origin", "--force-with-lease"}
				if tc.args.stack.noVerify {
					command = append(command, "--no-verify")
				}
				command = append(command, stack.Branches()...)
				mockCmd.EXPECT().
					GitWithIO(gomock.Any(), gomock.Any(), command).
					Return(tc.args.stack.pushErr)
			}

			err = opts.run(t.Context(), f, mockCmd)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tc.wantErrMsg)
				}
				if tc.wantErrIs != nil {
					require.ErrorIs(t, err, tc.wantErrIs)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCreateMRStreamsFailedHookOutput(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	opts := &options{
		io:     ios,
		target: glrepo.TestProject("stack_guy", "stackproject"),
	}
	ref := &git.StackRef{SHA: "1", Branch: "feature-branch"}
	hookErr := errors.New("create hook rejected push")

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().
		GitWithIO(
			ios.StdOut,
			ios.StdErr,
			"push",
			"--set-upstream",
			git.DefaultRemote,
			ref.Branch,
		).
		DoAndReturn(func(out, errOut io.Writer, _ ...string) error {
			_, err := io.WriteString(out, "hook stdout: create rejected\n")
			require.NoError(t, err)
			_, err = io.WriteString(errOut, "hook stderr: create rejected\n")
			require.NoError(t, err)
			return hookErr
		})

	_, err := createMR(nil, opts, ref, mockGit)

	require.Error(t, err)
	require.ErrorIs(t, err, hookErr)
	assert.Contains(t, err.Error(), "error pushing branch")
	assert.Contains(t, stdout.String(), "hook stdout: create rejected")
	assert.Contains(t, stderr.String(), "hook stderr: create rejected")
}

func TestForcePushAllWithLeaseStreamsFailedHookOutput(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	opts := &options{io: ios}
	stack := &git.Stack{
		Title: "test-stack",
		Refs: map[string]git.StackRef{
			"1": {SHA: "1", Branch: "feature-branch"},
		},
	}
	hookErr := errors.New("force hook rejected push")

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().
		GitWithIO(
			ios.StdOut,
			ios.StdErr,
			"push",
			git.DefaultRemote,
			"--force-with-lease",
			"feature-branch",
		).
		DoAndReturn(func(out, errOut io.Writer, _ ...string) error {
			_, err := io.WriteString(out, "hook stdout: force rejected\n")
			require.NoError(t, err)
			_, err = io.WriteString(errOut, "hook stderr: force rejected\n")
			require.NoError(t, err)
			return hookErr
		})

	err := forcePushAllWithLease(opts, stack, mockGit)

	require.Error(t, err)
	require.ErrorIs(t, err, hookErr)
	assert.Contains(t, stdout.String(), "hook stdout: force rejected")
	assert.Contains(t, stderr.String(), "hook stderr: force rejected")
}

func TestForcePushAllWithLeaseStreamsSuccessfulOutputInOrder(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	opts := &options{io: ios}
	stack := &git.Stack{
		Title: "test-stack",
		Refs: map[string]git.StackRef{
			"1": {SHA: "1", Branch: "feature-branch"},
		},
	}

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().
		GitWithIO(
			ios.StdOut,
			ios.StdErr,
			"push",
			git.DefaultRemote,
			"--force-with-lease",
			"feature-branch",
		).
		DoAndReturn(func(out, errOut io.Writer, _ ...string) error {
			_, err := io.WriteString(out, "hook stdout: force accepted\n")
			require.NoError(t, err)
			_, err = io.WriteString(errOut, "hook stderr: force accepted\n")
			require.NoError(t, err)
			return nil
		})

	err := forcePushAllWithLease(opts, stack, mockGit)

	require.NoError(t, err)

	stdoutText := stdout.String()
	assert.Contains(t, stdoutText, "  feature-branch\nhook stdout: force accepted\n")

	updatingIndex := strings.Index(stdoutText, "Updating branches")
	hookIndex := strings.Index(stdoutText, "hook stdout: force accepted")
	successIndex := strings.Index(stdoutText, "Push succeeded")
	require.GreaterOrEqual(t, updatingIndex, 0)
	require.GreaterOrEqual(t, hookIndex, 0)
	require.GreaterOrEqual(t, successIndex, 0)
	assert.Greater(t, hookIndex, updatingIndex)
	assert.Greater(t, successIndex, hookIndex)

	assert.Equal(t, 1, strings.Count(stdoutText, "Updating branches"))
	assert.Equal(t, 1, strings.Count(stdoutText, "hook stdout: force accepted"))
	assert.Equal(t, 1, strings.Count(stdoutText, "Push succeeded"))
	assert.NotContains(t, stdoutText, "Push succeeded:")
	assert.NotContains(t, stdoutText, "Push succeeded\n\n")

	stderrText := stderr.String()
	assert.Equal(t, 1, strings.Count(stderrText, "hook stderr: force accepted"))
	assert.NotContains(t, stderrText, "Push succeeded")
}

func createStack(t *testing.T, title string, scenario map[string]TestRef) {
	t.Helper()
	_ = git.CheckoutNewBranch("main")

	for _, ref := range scenario {
		err := git.AddStackRefFile(title, ref.ref)
		require.NoError(t, err)

		err = git.CheckoutNewBranch(ref.ref.Branch)
		require.NoError(t, err)
	}
}
