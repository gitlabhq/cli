//go:build !integration

package reorder

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_matchBranchesToStack(t *testing.T) {
	type args struct {
		stack    git.Stack
		branches []string
	}
	tests := []struct {
		name     string
		args     args
		expected git.Stack
		wantErr  bool
	}{
		{
			name: "basic situation",
			args: args{
				stack: git.Stack{
					Refs: map[string]git.StackRef{
						"123": {SHA: "123", Prev: "", Next: "456", Branch: "Branch1", Description: "blah1"},
						"456": {SHA: "456", Prev: "123", Next: "789", Branch: "Branch2", Description: "blah2"},
						"789": {SHA: "789", Prev: "456", Next: "", Branch: "Branch3", Description: "blah3"},
					},
				},
				branches: []string{"Branch2", "Branch3", "Branch1"},
			},
			expected: git.Stack{
				Refs: map[string]git.StackRef{
					"456": {SHA: "456", Prev: "", Next: "789", Branch: "Branch2", Description: "blah2"},
					"789": {SHA: "789", Prev: "456", Next: "123", Branch: "Branch3", Description: "blah3"},
					"123": {SHA: "123", Prev: "789", Next: "", Branch: "Branch1", Description: "blah1"},
				},
			},
		},

		{
			name: "missing branches from reordered list",
			args: args{
				stack: git.Stack{
					Refs: map[string]git.StackRef{
						"123": {SHA: "123", Prev: "", Next: "456", Branch: "Branch1"},
						"456": {SHA: "456", Prev: "123", Next: "789", Branch: "Branch2"},
						"789": {SHA: "789", Prev: "456", Next: "", Branch: "Branch3"},
					},
				},
				branches: []string{"Branch2", "Branch1"},
			},
			expected: git.Stack{},
			wantErr:  true,
		},

		{
			name: "large stack",
			args: args{
				stack: git.Stack{
					Refs: map[string]git.StackRef{
						"1":  {SHA: "1", Prev: "", Next: "2", Branch: "Branch1"},
						"2":  {SHA: "2", Prev: "1", Next: "3", Branch: "Branch2"},
						"3":  {SHA: "3", Prev: "2", Next: "4", Branch: "Branch3"},
						"4":  {SHA: "4", Prev: "3", Next: "5", Branch: "Branch4"},
						"5":  {SHA: "5", Prev: "4", Next: "6", Branch: "Branch5"},
						"6":  {SHA: "6", Prev: "5", Next: "7", Branch: "Branch6"},
						"7":  {SHA: "7", Prev: "6", Next: "8", Branch: "Branch7"},
						"8":  {SHA: "8", Prev: "7", Next: "9", Branch: "Branch8"},
						"9":  {SHA: "9", Prev: "8", Next: "10", Branch: "Branch9"},
						"10": {SHA: "10", Prev: "9", Next: "11", Branch: "Branch10"},
						"11": {SHA: "11", Prev: "10", Next: "12", Branch: "Branch11"},
						"12": {SHA: "12", Prev: "11", Next: "13", Branch: "Branch12"},
						"13": {SHA: "13", Prev: "12", Next: "", Branch: "Branch13"},
					},
				},
				branches: []string{
					"Branch12",
					"Branch1",
					"Branch2",
					"Branch8",
					"Branch11",
					"Branch3",
					"Branch6",
					"Branch9",
					"Branch7",
					"Branch5",
					"Branch10",
					"Branch13",
					"Branch4",
				},
			},
			expected: git.Stack{
				Refs: map[string]git.StackRef{
					"12": {SHA: "12", Prev: "", Next: "1", Branch: "Branch12"},
					"1":  {SHA: "1", Prev: "12", Next: "2", Branch: "Branch1"},
					"2":  {SHA: "2", Prev: "1", Next: "8", Branch: "Branch2"},
					"8":  {SHA: "8", Prev: "2", Next: "11", Branch: "Branch8"},
					"11": {SHA: "11", Prev: "8", Next: "3", Branch: "Branch11"},
					"3":  {SHA: "3", Prev: "11", Next: "6", Branch: "Branch3"},
					"6":  {SHA: "6", Prev: "3", Next: "9", Branch: "Branch6"},
					"9":  {SHA: "9", Prev: "6", Next: "7", Branch: "Branch9"},
					"7":  {SHA: "7", Prev: "9", Next: "5", Branch: "Branch7"},
					"5":  {SHA: "5", Prev: "7", Next: "10", Branch: "Branch5"},
					"10": {SHA: "10", Prev: "5", Next: "13", Branch: "Branch10"},
					"13": {SHA: "13", Prev: "10", Next: "4", Branch: "Branch13"},
					"4":  {SHA: "4", Prev: "13", Next: "", Branch: "Branch4"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git.InitGitRepo(t)

			err := git.CreateRefFiles(tt.args.stack.Refs, "cool stack")
			require.NoError(t, err)

			git.CreateBranches(t, tt.args.branches)

			newStack, err := matchBranchesToStack(tt.args.stack, tt.args.branches)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				for k, ref := range tt.expected.Refs {
					require.Equal(t, newStack.Refs[k], ref)
				}

				require.Len(t, newStack.Refs, len(tt.args.branches))
			}
		})
	}
}

func setupTestFactoryForReorder(t *testing.T, testClient *gitlabtesting.TestClient) cmdutils.Factory {
	t.Helper()

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))

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
	)

	return f
}

func Test_updateMRs(t *testing.T) {
	type args struct {
		newStack   git.Stack
		oldStack   git.Stack
		setupMocks func(t *testing.T, testClient *gitlabtesting.TestClient)
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "update a complex stack",
			args: args{
				newStack: git.Stack{
					Refs: map[string]git.StackRef{
						"7": {
							SHA: "7", Prev: "", Next: "5", Branch: "Branch7",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/7",
						},
						"5": {
							SHA: "5", Prev: "7", Next: "8", Branch: "Branch5",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/5",
						},
						"8": {
							SHA: "8", Prev: "5", Next: "1", Branch: "Branch8",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/8",
						},
						"1": {
							SHA: "1", Prev: "8", Next: "9", Branch: "Branch1",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
						},
						"9": {
							SHA: "9", Prev: "1", Next: "4", Branch: "Branch9",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/9",
						},
						"4": {
							SHA: "4", Prev: "9", Next: "2", Branch: "Branch4",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/4",
						},
						"2": {
							SHA: "2", Prev: "4", Next: "3", Branch: "Branch2",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/2",
						},
						"3": {
							SHA: "3", Prev: "2", Next: "6", Branch: "Branch3",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/3",
						},
						"6": {
							SHA: "6", Prev: "3", Next: "10", Branch: "Branch6",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/6",
						},
						"10": {
							SHA: "10", Prev: "6", Next: "12", Branch: "Branch10",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/10",
						},
						"12": {
							SHA: "12", Prev: "10", Next: "11", Branch: "Branch12",
							MR: "",
						},
						"11": {
							SHA: "11", Prev: "12", Next: "13", Branch: "Branch11",
							MR: "",
						},
						"13": {
							SHA: "13", Prev: "11", Next: "", Branch: "Branch13",
							MR: "",
						},
					},
				},

				oldStack: git.Stack{
					Refs: map[string]git.StackRef{
						"1": {
							SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
						},
						"2": {
							SHA: "2", Prev: "1", Next: "3", Branch: "Branch2",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/2",
						},
						"3": {
							SHA: "3", Prev: "2", Next: "4", Branch: "Branch3",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/3",
						},
						"4": {
							SHA: "4", Prev: "3", Next: "5", Branch: "Branch4",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/4",
						},
						"5": {
							SHA: "5", Prev: "4", Next: "6", Branch: "Branch5",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/5",
						},
						"6": {
							SHA: "6", Prev: "5", Next: "7", Branch: "Branch6",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/6",
						},
						"7": {
							SHA: "7", Prev: "6", Next: "8", Branch: "Branch7",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/7",
						},
						"8": {
							SHA: "8", Prev: "7", Next: "9", Branch: "Branch8",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/8",
						},
						"9": {
							SHA: "9", Prev: "8", Next: "10", Branch: "Branch9",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/9",
						},
						"10": {
							SHA: "10", Prev: "9", Next: "11", Branch: "Branch10",
							MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/10",
						},
						"11": {
							SHA: "11", Prev: "10", Next: "12", Branch: "Branch11",
							MR: "",
						},
						"12": {
							SHA: "12", Prev: "11", Next: "13", Branch: "Branch12",
							MR: "",
						},
						"13": {
							SHA: "13", Prev: "12", Next: "", Branch: "Branch13",
							MR: "",
						},
					},
				},
				setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
					t.Helper()
					// For each branch with an MR, we need:
					// 1. ListProjectMergeRequests (open MRs by source branch)
					// 2. GetMergeRequest
					// 3. UpdateMergeRequest (to change target branch)

					// GetProject is called once for the first ref (Prev == "") to get the default branch
					testClient.MockProjects.EXPECT().
						GetProject("stack_guy/stackproject", gomock.Any()).
						Return(&gitlab.Project{
							ID:            3,
							DefaultBranch: "main",
						}, nil, nil)

					branchesToUpdate := []struct {
						branch    string
						iid       int64
						newTarget string
					}{
						{"Branch7", 7, "main"},
						{"Branch5", 5, "Branch7"},
						{"Branch8", 8, "Branch5"},
						{"Branch1", 1, "Branch8"},
						{"Branch9", 9, "Branch1"},
						{"Branch4", 4, "Branch9"},
						{"Branch2", 2, "Branch4"},
						{"Branch3", 3, "Branch2"},
						{"Branch6", 6, "Branch3"},
						{"Branch10", 10, "Branch6"},
					}

					for _, b := range branchesToUpdate {
						branch := b.branch
						iid := b.iid
						newTarget := b.newTarget

						// MockListOpenStackMRsByBranch
						testClient.MockMergeRequests.EXPECT().
							ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
							DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
								assert.Equal(t, branch, *opts.SourceBranch)
								assert.Equal(t, "opened", *opts.State)
								return []*gitlab.BasicMergeRequest{
									{
										ID:           iid,
										IID:          iid,
										ProjectID:    3,
										SourceBranch: branch,
										State:        "opened",
									},
								}, nil, nil
							})

						// MockGetStackMR
						testClient.MockMergeRequests.EXPECT().
							GetMergeRequest("stack_guy/stackproject", iid, gomock.Any()).
							Return(&gitlab.MergeRequest{
								BasicMergeRequest: gitlab.BasicMergeRequest{
									ID:           iid,
									IID:          iid,
									ProjectID:    3,
									SourceBranch: branch,
									State:        "opened",
								},
							}, nil, nil)

						// MockPutStackMR (UpdateMergeRequest)
						// Note: UpdateMergeRequest is called with mr.ProjectID (int64) not the string path
						testClient.MockMergeRequests.EXPECT().
							UpdateMergeRequest(int64(3), iid, gomock.Any()).
							DoAndReturn(func(pid any, mrIID int64, opts *gitlab.UpdateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
								assert.Equal(t, newTarget, *opts.TargetBranch)
								return &gitlab.MergeRequest{
									BasicMergeRequest: gitlab.BasicMergeRequest{
										IID:          iid,
										TargetBranch: newTarget,
									},
								}, nil, nil
							})
					}
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git.InitGitRepoWithCommit(t)

			gitAddRemote := git.GitCommand("remote", "add", "origin", "http://gitlab.com/gitlab-org/cli.git")
			_, err := run.PrepareCmd(gitAddRemote).Output()
			require.NoError(t, err)

			testClient := gitlabtesting.NewTestClient(t)
			tt.args.setupMocks(t, testClient)

			factory := setupTestFactoryForReorder(t, testClient)

			err = updateMRs(t.Context(), factory, tt.args.newStack, tt.args.oldStack)

			require.NoError(t, err)
		})
	}
}
