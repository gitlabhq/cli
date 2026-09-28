//go:build !integration

package reorder

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	gittesting "gitlab.com/gitlab-org/cli/internal/git/testing"
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

			onDisk, gatherErr := git.GatherStackRefs("cool stack")
			require.NoError(t, gatherErr)
			require.Equal(t, tt.args.stack.Refs, onDisk.Refs, "ref files must not change until the rebases finish")

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

	return cmdtest.NewTestFactory(ios, reorderFactoryOptions(t, testClient)...)
}

// reorderFactoryOptions wires the mock client into the factory. The config
// needs a host because the reorder MR update checks for one before any API call.
func reorderFactoryOptions(t *testing.T, testClient *gitlabtesting.TestClient) []cmdtest.FactoryOption {
	t.Helper()

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: ""}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	return []cmdtest.FactoryOption{
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithConfig(config.NewFromString("hosts:\n  gitlab.com:\n    token: test-token\n")),
		func(f *cmdtest.Factory) {
			f.BaseRepoStub = func() (glrepo.Interface, error) {
				return glrepo.TestProject("stack_guy", "stackproject"), nil
			}
			f.ApiClientStub = func(repoHost string) (*api.Client, error) {
				return apiClient, nil
			}
		},
	}
}

type retarget struct {
	iid    int64
	target string
}

// expectRetargets mocks the lookup and target-branch update for each branch's MR.
// Lookups are matched by source branch, so the order updateMRs visits them in does not matter.
func expectRetargets(t *testing.T, testClient *gitlabtesting.TestClient, want map[string]retarget) {
	t.Helper()

	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
		Times(len(want)).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			r, ok := want[*opts.SourceBranch]
			require.True(t, ok, "unexpected MR lookup for branch %q", *opts.SourceBranch)
			return []*gitlab.BasicMergeRequest{{
				ID: r.iid, IID: r.iid, ProjectID: 3, SourceBranch: *opts.SourceBranch, State: "opened",
			}}, nil, nil
		})

	for branch, r := range want {
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("stack_guy/stackproject", r.iid, gomock.Any()).
			Return(&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				ID: r.iid, IID: r.iid, ProjectID: 3, SourceBranch: branch, State: "opened",
			}}, nil, nil)

		testClient.MockMergeRequests.EXPECT().
			UpdateMergeRequest(int64(3), r.iid, gomock.Any()).
			DoAndReturn(func(pid any, iid int64, opts *gitlab.UpdateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
				assert.Equal(t, r.target, *opts.TargetBranch, "target branch for %s", branch)
				return &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{IID: iid, TargetBranch: r.target}}, nil, nil
			})
	}
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

			err = updateMRs(t.Context(), factory, testClient.Client, "main", tt.args.newStack, tt.args.oldStack)

			require.NoError(t, err)
		})
	}
}

// gitExec is a helper that runs a git command and fails the test on error.
func gitExec(t *testing.T, args ...string) string {
	t.Helper()
	cmd := git.GitCommand(args...)
	out, err := run.PrepareCmd(cmd).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func Test_rebaseInNewOrder(t *testing.T) {
	t.Run("reorders branches A,B,C to C,A,B", func(t *testing.T) {
		git.InitGitRepoWithCommit(t)
		gr := git.StandardGitCommand{}

		// The default branch from InitGitRepoWithCommit may not be "main",
		// so create it if it doesn't exist, or just check it out.
		_ = run.PrepareCmd(git.GitCommand("branch", "-M", "main")).Run()

		// Create branch A off main with a unique commit
		gitExec(t, "checkout", "-b", "branchA")
		require.NoError(t, os.WriteFile("fileA", []byte("A content"), 0o644))
		gitExec(t, "add", "fileA")
		gitExec(t, "commit", "-m", "commit A")

		// Create branch B off A with a unique commit
		gitExec(t, "checkout", "-b", "branchB")
		require.NoError(t, os.WriteFile("fileB", []byte("B content"), 0o644))
		gitExec(t, "add", "fileB")
		gitExec(t, "commit", "-m", "commit B")

		// Create branch C off B with a unique commit
		gitExec(t, "checkout", "-b", "branchC")
		require.NoError(t, os.WriteFile("fileC", []byte("C content"), 0o644))
		gitExec(t, "add", "fileC")
		gitExec(t, "commit", "-m", "commit C")

		// Go back to branchA (simulating user's current branch)
		gitExec(t, "checkout", "branchA")

		// Old stack: A -> B -> C
		oldStack := git.Stack{
			Title: "test-stack",
			Refs: map[string]git.StackRef{
				"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
				"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "ref-c", Branch: "branchB"},
				"ref-c": {SHA: "ref-c", Prev: "ref-b", Next: "", Branch: "branchC"},
			},
		}

		// New stack: C -> A -> B
		newStack := git.Stack{
			Title: "test-stack",
			Refs: map[string]git.StackRef{
				"ref-c": {SHA: "ref-c", Prev: "", Next: "ref-a", Branch: "branchC"},
				"ref-a": {SHA: "ref-a", Prev: "ref-c", Next: "ref-b", Branch: "branchA"},
				"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
			},
		}

		// Write stack ref files (creates the stack directory) and base branch metadata
		require.NoError(t, git.CreateRefFiles(oldStack.Refs, "test-stack"))
		require.NoError(t, git.AddStackBaseBranch("test-stack", "main"))

		_, err := rebaseInNewOrder(gr, oldStack, newStack)
		require.NoError(t, err)

		// Verify we're back on branchA
		currentBranch, err := git.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "branchA", currentBranch)

		mainTip := gitExec(t, "rev-parse", "main")

		// branchC should be directly on top of main, containing only fileC
		// (not fileA or fileB from the old order)
		cParent := gitExec(t, "rev-parse", "branchC~1")
		assert.Equal(t, mainTip, cParent, "branchC's parent should be main")
		cLog := gitExec(t, "log", "--oneline", "main..branchC")
		assert.Equal(t, 1, strings.Count(cLog, "\n")+1, "branchC should have exactly 1 commit on top of main")

		// branchA should be on top of branchC
		aParent := gitExec(t, "rev-parse", "branchA~1")
		cTip := gitExec(t, "rev-parse", "branchC")
		assert.Equal(t, cTip, aParent, "branchA's parent should be branchC")

		// branchB should be on top of branchA
		bParent := gitExec(t, "rev-parse", "branchB~1")
		aTip := gitExec(t, "rev-parse", "branchA")
		assert.Equal(t, aTip, bParent, "branchB's parent should be branchA")

		// Verify each branch has the right files
		gitExec(t, "checkout", "branchC")
		_, errStat := os.Stat("fileC")
		require.NoError(t, errStat, "branchC should have fileC")

		gitExec(t, "checkout", "branchA")
		_, errStat = os.Stat("fileA")
		require.NoError(t, errStat, "branchA should have fileA")
		_, errStat = os.Stat("fileC")
		require.NoError(t, errStat, "branchA should have fileC (inherited from branchC)")

		gitExec(t, "checkout", "branchB")
		_, errStat = os.Stat("fileB")
		require.NoError(t, errStat, "branchB should have fileB")
		_, errStat = os.Stat("fileA")
		require.NoError(t, errStat, "branchB should have fileA (inherited from branchA)")
	})

	t.Run("conflict produces useful error", func(t *testing.T) {
		oldStack := git.Stack{
			Title: "test-stack",
			Refs: map[string]git.StackRef{
				"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
				"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
			},
		}

		newStack := git.Stack{
			Title: "test-stack",
			Refs: map[string]git.StackRef{
				"ref-b": {SHA: "ref-b", Prev: "", Next: "ref-a", Branch: "branchB"},
				"ref-a": {SHA: "ref-a", Prev: "ref-b", Next: "", Branch: "branchA"},
			},
		}

		// Need a git repo with a commit for CurrentBranch() to work
		git.InitGitRepoWithCommit(t)
		require.NoError(t, git.CreateRefFiles(oldStack.Refs, "test-stack"))
		require.NoError(t, git.AddStackBaseBranch("test-stack", "main"))

		ctrl := gomock.NewController(t)
		mockGr := gittesting.NewMockGitRunner(ctrl)

		gomock.InOrder(
			mockGr.EXPECT().Git("rev-parse", "branchA").Return("aaaa\n", nil),
			mockGr.EXPECT().Git("rev-parse", "branchB").Return("bbbb\n", nil),
			mockGr.EXPECT().Git("rev-parse", "main").Return("0000\n", nil),
			// checkout branchB (first in new order)
			mockGr.EXPECT().Git("checkout", "branchB").Return("", nil),
			// rebase stops on a conflict, leaving a rebase in progress
			mockGr.EXPECT().Git("rebase", "--onto", "main", "aaaa").
				DoAndReturn(func(...string) (string, error) {
					require.NoError(t, os.MkdirAll(".git/rebase-merge", 0o755))
					return "", fmt.Errorf("conflict")
				}),
		)

		_, err := rebaseInNewOrder(mockGr, oldStack, newStack)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rebase conflict on branchB onto main")
		assert.Contains(t, err.Error(), "glab stack reorder --continue")
		assert.Contains(t, err.Error(), "glab stack reorder --abort")

		// Verify state file was persisted with correct NextIndex
		state, stateErr := git.ReadReorderState("test-stack")
		require.NoError(t, stateErr)
		assert.Equal(t, 0, state.NextIndex, "NextIndex should point to the failed branch")
	})

	t.Run("refused rebase reports git's error, not a conflict", func(t *testing.T) {
		oldStack := git.Stack{
			Title: "test-stack",
			Refs: map[string]git.StackRef{
				"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
				"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
			},
		}

		newStack := git.Stack{
			Title: "test-stack",
			Refs: map[string]git.StackRef{
				"ref-b": {SHA: "ref-b", Prev: "", Next: "ref-a", Branch: "branchB"},
				"ref-a": {SHA: "ref-a", Prev: "ref-b", Next: "", Branch: "branchA"},
			},
		}

		git.InitGitRepoWithCommit(t)
		require.NoError(t, git.CreateRefFiles(oldStack.Refs, "test-stack"))
		require.NoError(t, git.AddStackBaseBranch("test-stack", "main"))

		ctrl := gomock.NewController(t)
		mockGr := gittesting.NewMockGitRunner(ctrl)

		gomock.InOrder(
			mockGr.EXPECT().Git("rev-parse", "branchA").Return("aaaa\n", nil),
			mockGr.EXPECT().Git("rev-parse", "branchB").Return("bbbb\n", nil),
			mockGr.EXPECT().Git("rev-parse", "main").Return("0000\n", nil),
			mockGr.EXPECT().Git("checkout", "branchB").Return("", nil),
			// git refuses to start, so no rebase is left in progress
			mockGr.EXPECT().Git("rebase", "--onto", "main", "aaaa").
				Return("", fmt.Errorf("cannot rebase: You have unstaged changes")),
		)

		_, err := rebaseInNewOrder(mockGr, oldStack, newStack)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not rebase branchB onto main")
		assert.Contains(t, err.Error(), "cannot rebase: You have unstaged changes")
		assert.Contains(t, err.Error(), "glab stack reorder --abort")
		assert.NotContains(t, err.Error(), "rebase conflict")

		state, stateErr := git.ReadReorderState("test-stack")
		require.NoError(t, stateErr)
		assert.Equal(t, 0, state.NextIndex, "state must be kept so --abort can restore the branches")
	})
}

func Test_normalFlow_uncommittedChanges_errorsBeforeEditor(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "", Branch: "branchA"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)
	mockGr.EXPECT().Git("status", "--porcelain", "--untracked-files=no").Return(" M file\n", nil)

	// A nil editor func panics if called, so reaching it fails the test.
	err := runReorderCmd(t, mockGr, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uncommitted changes")
}

func Test_continueFlow_uncommittedChanges_errors(t *testing.T) {
	git.InitGitRepoWithCommit(t)
	writePausedReorder(t)

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)
	mockGr.EXPECT().Git("status", "--porcelain", "--untracked-files=no").Return(" M file\n", nil)

	err := runReorderCmd(t, mockGr, "--continue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uncommitted changes")

	got, readErr := git.ReadReorderState("test-stack")
	require.NoError(t, readErr)
	assert.Equal(t, 1, got.NextIndex)
}

func Test_executeRebases_resumesFromNextIndex(t *testing.T) {
	git.InitGitRepoWithCommit(t)
	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
	}, "test-stack"))
	require.NoError(t, git.AddStackBaseBranch("test-stack", "main"))

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)

	// State simulates: branchB already done (NextIndex=1), branchA still to do
	state := git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      1,
		OldTips:        map[string]string{"branchA": "aaaa", "branchB": "bbbb", "main": "0000"},
		OldParent:      map[string]string{"branchA": "main", "branchB": "branchA"},
		BaseBranch:     "main",
		OriginalBranch: "branchA",
	}

	// Should only rebase branchA (index 1), not branchB (index 0)
	gomock.InOrder(
		mockGr.EXPECT().Git("checkout", "branchA").Return("", nil),
		mockGr.EXPECT().Git("rebase", "--onto", "branchB", "0000").Return("", nil),
		mockGr.EXPECT().Git("checkout", "branchA").Return("", nil), // restore original
	)

	err := executeRebases(mockGr, "test-stack", &state)
	require.NoError(t, err)
	assert.Equal(t, 2, state.NextIndex)
}

func Test_abortFlow_resetsAndRestores(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	oldRefs := map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
	}

	require.NoError(t, git.CreateRefFiles(oldRefs, "test-stack"))
	require.NoError(t, git.AddStackBaseBranch("test-stack", "main"))

	// Write a state file as if a reorder is in progress
	state := git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      1,
		OldTips:        map[string]string{"branchA": "aaaa", "branchB": "bbbb", "main": "0000"},
		OldParent:      map[string]string{"branchA": "main", "branchB": "branchA"},
		BaseBranch:     "main",
		OriginalBranch: "branchA",
		OldRefs:        oldRefs,
	}
	require.NoError(t, git.WriteReorderState("test-stack", state))

	// Need to set glab.currentstack for GetCurrentStackTitle
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)

	// The checked-out branch is reset with --keep; the rest move without a checkout.
	gomock.InOrder(
		mockGr.EXPECT().Git("rebase", "--abort").Return("", nil),
		mockGr.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("branchA\n", nil),
		mockGr.EXPECT().Git("reset", "--keep", "aaaa").Return("", nil),
		mockGr.EXPECT().Git("branch", "-f", "branchB", "bbbb").Return("", nil),
		mockGr.EXPECT().Git("checkout", "branchA").Return("", nil), // restore original
	)

	err := runReorderCmd(t, mockGr, "--abort")
	require.NoError(t, err)

	// Verify state file was deleted
	inProgress, err := git.ReorderInProgress("test-stack")
	require.NoError(t, err)
	assert.False(t, inProgress)

	onDisk, err := git.GatherStackRefs("test-stack")
	require.NoError(t, err)
	assert.Equal(t, oldRefs, onDisk.Refs, "abort must leave the ref files in the old order")
}

func Test_abortFlow_detachedHead_movesEveryBranch(t *testing.T) {
	git.InitGitRepoWithCommit(t)
	writePausedReorder(t)

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)

	gomock.InOrder(
		mockGr.EXPECT().Git("rebase", "--abort").Return("", nil),
		mockGr.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("", fmt.Errorf("exit status 1")),
		mockGr.EXPECT().Git("branch", "-f", "branchA", "aaaa").Return("", nil),
		mockGr.EXPECT().Git("branch", "-f", "branchB", "bbbb").Return("", nil),
		mockGr.EXPECT().Git("checkout", "branchA").Return("", nil),
	)

	require.NoError(t, runReorderCmd(t, mockGr, "--abort"))
}

func Test_abortFlow_uncommittedChanges_stopsBeforeMovingBranches(t *testing.T) {
	git.InitGitRepoWithCommit(t)
	writePausedReorder(t)

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)

	// No `branch -f` expectations: a refused reset must leave the other branches alone.
	gomock.InOrder(
		mockGr.EXPECT().Git("rebase", "--abort").Return("", nil),
		mockGr.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("branchA\n", nil),
		mockGr.EXPECT().Git("reset", "--keep", "aaaa").Return("", fmt.Errorf("entry 'f' not uptodate. Cannot merge")),
	)

	err := runReorderCmd(t, mockGr, "--abort")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Commit or stash them")

	inProgress, err := git.ReorderInProgress("test-stack")
	require.NoError(t, err)
	assert.True(t, inProgress, "state must be kept so --abort can be retried")
}

func writePausedReorder(t *testing.T) {
	t.Helper()

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))
	require.NoError(t, git.WriteReorderState("test-stack", git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      1,
		OldTips:        map[string]string{"branchA": "aaaa", "branchB": "bbbb", "main": "0000"},
		BaseBranch:     "main",
		OriginalBranch: "branchA",
	}))
}

func Test_abortFlow_noStateFile(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "", Branch: "branchA"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	err := runReorderCmd(t, git.StandardGitCommand{}, "--abort")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reorder in progress")
}

func Test_abortFlow_refusedAfterRebasesComplete(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	state := git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      2,
		BaseBranch:     "main",
		OriginalBranch: "branchA",
	}
	require.NoError(t, git.WriteReorderState("test-stack", state))

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)

	err := runReorderCmd(t, mockGr, "--abort")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot abort")
	assert.Contains(t, err.Error(), "glab stack reorder --continue")

	inProgress, err := git.ReorderInProgress("test-stack")
	require.NoError(t, err)
	assert.True(t, inProgress, "state must be kept so --continue can finish the reorder")
}

func Test_normalFlow_blockedByInProgressReorder(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "", Branch: "branchA"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	// Write a state file to simulate an in-progress reorder.
	state := git.ReorderState{NewOrder: []string{"branchA"}}
	require.NoError(t, git.WriteReorderState("test-stack", state))

	// A fresh `glab stack reorder` must refuse to start while one is in progress.
	err := runReorderCmd(t, git.StandardGitCommand{}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a stack reorder is in progress")
}

func Test_continueFlow_noStateFile(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "", Branch: "branchA"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	// No state file written, so there is nothing to continue.
	err := runReorderCmd(t, git.StandardGitCommand{}, "--continue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reorder in progress")
}

func Test_continueFlow_invalidIndex(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "", Branch: "branchA"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	// NextIndex points past the end of NewOrder — a corrupt/invalid state.
	state := git.ReorderState{NewOrder: []string{"branchA"}, NextIndex: 2}
	require.NoError(t, git.WriteReorderState("test-stack", state))

	err := runReorderCmd(t, git.StandardGitCommand{}, "--continue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func Test_continueFlow_gitRebaseInProgress_errors(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	state := git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      0,
		BaseBranch:     "main",
		OriginalBranch: "branchA",
	}
	require.NoError(t, git.WriteReorderState("test-stack", state))

	require.NoError(t, os.MkdirAll(".git/rebase-merge", 0o755))

	// No expectations: --continue must not run `git rebase --continue` itself.
	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)

	err := runReorderCmd(t, mockGr, "--continue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still in progress")
	assert.Contains(t, err.Error(), "git rebase --continue")

	got, readErr := git.ReadReorderState("test-stack")
	require.NoError(t, readErr)
	assert.Equal(t, 0, got.NextIndex)
}

// Test_continueFlow_branchNotRebasedOntoParent_errors covers the guard that
// stops --continue from silently skipping a branch that was never rebased onto
// its new parent (e.g. the user aborted the git rebase). It must error and
// leave NextIndex untouched rather than advancing.
func Test_continueFlow_branchNotRebasedOntoParent_errors(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	// Reorder stalled at index 0: branchB should be rebased onto main first.
	state := git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      0,
		OldTips:        map[string]string{"branchA": "aaaa", "branchB": "bbbb", "main": "0000"},
		BaseBranch:     "main",
		OriginalBranch: "branchA",
	}
	require.NoError(t, git.WriteReorderState("test-stack", state))

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)
	// branchB moved, but a non-zero `merge-base --is-ancestor` exit (returned
	// here as an error) means it did not land on main.
	gomock.InOrder(
		mockGr.EXPECT().Git("status", "--porcelain", "--untracked-files=no").Return("", nil),
		mockGr.EXPECT().Git("rev-parse", "branchB").Return("cccc\n", nil),
		mockGr.EXPECT().
			Git("merge-base", "--is-ancestor", "main", "branchB").
			Return("", fmt.Errorf("exit status 1")),
	)

	err := runReorderCmd(t, mockGr, "--continue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `branch "branchB" is not rebased onto "main"`)

	// The state must not have advanced past the unrebased branch.
	got, readErr := git.ReadReorderState("test-stack")
	require.NoError(t, readErr)
	assert.Equal(t, 0, got.NextIndex, "NextIndex must not advance when the branch is not rebased")
}

// After `git rebase --abort` the branch is back at its old tip. The new parent
// can still be an ancestor (always, for the base branch), so --continue must
// not advance on the ancestor check alone.
func Test_continueFlow_branchStillAtOldTip_errors(t *testing.T) {
	git.InitGitRepoWithCommit(t)

	require.NoError(t, git.CreateRefFiles(map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
	}, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	state := git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      0,
		OldTips:        map[string]string{"branchA": "aaaa", "branchB": "bbbb", "main": "0000"},
		BaseBranch:     "main",
		OriginalBranch: "branchA",
	}
	require.NoError(t, git.WriteReorderState("test-stack", state))

	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)
	// No merge-base expectation: an unmoved tip must fail before the ancestor check.
	gomock.InOrder(
		mockGr.EXPECT().Git("status", "--porcelain", "--untracked-files=no").Return("", nil),
		mockGr.EXPECT().Git("rev-parse", "branchB").Return("bbbb\n", nil),
	)

	err := runReorderCmd(t, mockGr, "--continue")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `branch "branchB" is not rebased onto "main"`)

	got, readErr := git.ReadReorderState("test-stack")
	require.NoError(t, readErr)
	assert.Equal(t, 0, got.NextIndex)
}

// Picks up where a user leaves off after resolving a conflict: the conflicted
// branch is already rebased by hand, and --continue must rebase the rest,
// write the new ref files, retarget every moved MR, and clear the state.
func Test_continueFlow_afterResolvedConflict_finishesReorder(t *testing.T) {
	git.InitGitRepoWithCommit(t)
	gr := git.StandardGitCommand{}
	gitExec(t, "branch", "-M", "main")
	gitExec(t, "remote", "add", "origin", "http://gitlab.com/stack_guy/stackproject.git")

	for _, b := range []string{"branchA", "branchB", "branchC"} {
		gitExec(t, "checkout", "-b", b)
		require.NoError(t, os.WriteFile("file"+b, []byte(b), 0o644))
		gitExec(t, "add", "file"+b)
		gitExec(t, "commit", "-m", "commit "+b)
	}
	gitExec(t, "checkout", "branchA")

	mr := func(iid string) string { return "http://gitlab.com/stack_guy/stackproject/-/merge_requests/" + iid }
	oldStack := git.Stack{Title: "test-stack", Refs: map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA", MR: mr("1")},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "ref-c", Branch: "branchB", MR: mr("2")},
		"ref-c": {SHA: "ref-c", Prev: "ref-b", Next: "", Branch: "branchC", MR: mr("3")},
	}}
	newStack := git.Stack{Title: "test-stack", Refs: map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-c", Branch: "branchA", MR: mr("1")},
		"ref-c": {SHA: "ref-c", Prev: "ref-a", Next: "ref-b", Branch: "branchC", MR: mr("3")},
		"ref-b": {SHA: "ref-b", Prev: "ref-c", Next: "", Branch: "branchB", MR: mr("2")},
	}}
	require.NoError(t, git.CreateRefFiles(oldStack.Refs, "test-stack"))
	require.NoError(t, git.AddStackBaseBranch("test-stack", "main"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

	// State as the conflict on branchC (index 1) left it, after the user
	// resolved it and finished `git rebase --continue`.
	state, err := prepareReorderState(gr, oldStack, newStack)
	require.NoError(t, err)
	gitExec(t, "checkout", "branchC")
	gitExec(t, "rebase", "--onto", "branchA", state.OldTips["branchB"])
	state.NextIndex = 1
	require.NoError(t, git.WriteReorderState("test-stack", state))

	testClient := gitlabtesting.NewTestClient(t)
	expectRetargets(t, testClient, map[string]retarget{
		"branchA": {iid: 1, target: "main"},
		"branchC": {iid: 3, target: "branchA"},
		"branchB": {iid: 2, target: "branchC"},
	})

	err = runReorderCmd(t, gr, "--continue", reorderFactoryOptions(t, testClient)...)
	require.NoError(t, err)

	assert.Equal(t, gitExec(t, "rev-parse", "branchA"), gitExec(t, "rev-parse", "branchC~1"), "branchC should sit on branchA")
	assert.Equal(t, gitExec(t, "rev-parse", "branchC"), gitExec(t, "rev-parse", "branchB~1"), "branchB should sit on branchC")

	currentBranch, err := git.CurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "branchA", currentBranch)

	onDisk, err := git.GatherStackRefs("test-stack")
	require.NoError(t, err)
	assert.Equal(t, newStack.Refs, onDisk.Refs)

	inProgress, err := git.ReorderInProgress("test-stack")
	require.NoError(t, err)
	assert.False(t, inProgress)
}

// Every rebase is done but the MR update failed, so --continue must only
// write the ref files and retarget the MRs, without running git.
func Test_continueFlow_rebasesDone_retriesMRUpdate(t *testing.T) {
	git.InitGitRepoWithCommit(t)
	gitExec(t, "remote", "add", "origin", "http://gitlab.com/stack_guy/stackproject.git")

	mr := func(iid string) string { return "http://gitlab.com/stack_guy/stackproject/-/merge_requests/" + iid }
	oldRefs := map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA", MR: mr("1")},
		"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB", MR: mr("2")},
	}
	newRefs := map[string]git.StackRef{
		"ref-b": {SHA: "ref-b", Prev: "", Next: "ref-a", Branch: "branchB", MR: mr("2")},
		"ref-a": {SHA: "ref-a", Prev: "ref-b", Next: "", Branch: "branchA", MR: mr("1")},
	}
	require.NoError(t, git.CreateRefFiles(oldRefs, "test-stack"))
	require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))
	require.NoError(t, git.WriteReorderState("test-stack", git.ReorderState{
		NewOrder:       []string{"branchB", "branchA"},
		NextIndex:      2,
		BaseBranch:     "main",
		OriginalBranch: "branchA",
		OldRefs:        oldRefs,
		NewRefs:        newRefs,
	}))

	testClient := gitlabtesting.NewTestClient(t)
	expectRetargets(t, testClient, map[string]retarget{
		"branchB": {iid: 2, target: "main"},
		"branchA": {iid: 1, target: "branchB"},
	})

	// No expectations: the finish-only path must not run git.
	ctrl := gomock.NewController(t)
	mockGr := gittesting.NewMockGitRunner(ctrl)

	err := runReorderCmd(t, mockGr, "--continue", reorderFactoryOptions(t, testClient)...)
	require.NoError(t, err)

	onDisk, err := git.GatherStackRefs("test-stack")
	require.NoError(t, err)
	assert.Equal(t, newRefs, onDisk.Refs)

	inProgress, err := git.ReorderInProgress("test-stack")
	require.NoError(t, err)
	assert.False(t, inProgress)
}

// runReorderCmd runs `glab stack reorder <args>` through the real command and factory.
func runReorderCmd(t *testing.T, gr git.GitRunner, args string, opts ...cmdtest.FactoryOption) error {
	t.Helper()

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdReorderStack(f, gr, nil)
	}, false, opts...)

	_, err := exec(args)
	return err
}
