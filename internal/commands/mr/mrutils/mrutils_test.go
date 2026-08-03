//go:build !integration

package mrutils

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_DisplayMR(t *testing.T) {
	testCases := []struct {
		name        string
		mr          *gitlab.BasicMergeRequest
		output      string
		outputToTTY bool
	}{
		{
			name: "opened",
			mr: &gitlab.BasicMergeRequest{
				IID:          1,
				State:        "opened",
				Title:        "This is open",
				SourceBranch: "main",
				WebURL:       "https://gitlab.com/gitlab-org/cli/-/merge_requests/1",
			},
			output:      "!1 This is open (main)\n https://gitlab.com/gitlab-org/cli/-/merge_requests/1\n",
			outputToTTY: true,
		},
		{
			name: "merged",
			mr: &gitlab.BasicMergeRequest{
				IID:          2,
				State:        "merged",
				Title:        "This is merged",
				SourceBranch: "main",
				WebURL:       "https://gitlab.com/gitlab-org/cli/-/merge_requests/2",
			},
			output:      "!2 This is merged (main)\n https://gitlab.com/gitlab-org/cli/-/merge_requests/2\n",
			outputToTTY: true,
		},
		{
			name: "closed",
			mr: &gitlab.BasicMergeRequest{
				IID:          3,
				State:        "closed",
				Title:        "This is closed",
				SourceBranch: "main",
				WebURL:       "https://gitlab.com/gitlab-org/cli/-/merge_requests/3",
			},
			output:      "!3 This is closed (main)\n https://gitlab.com/gitlab-org/cli/-/merge_requests/3\n",
			outputToTTY: true,
		},
		{
			name: "non-tty terse output",
			mr: &gitlab.BasicMergeRequest{
				IID:          4,
				State:        "open",
				Title:        "This shouldn't be visible",
				SourceBranch: "main",
				WebURL:       "https://gitlab.com/gitlab-org/cli/-/merge_requests/4",
			},
			output:      "https://gitlab.com/gitlab-org/cli/-/merge_requests/4",
			outputToTTY: false,
		},
	}
	streams, _, _, _ := cmdtest.TestIOStreams()
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := DisplayMR(streams.Color(), tC.mr, tC.outputToTTY)
			assert.Equal(t, tC.output, got)
		})
	}
}

func TestGenerateMRTitleAndBody(t *testing.T) {
	tests := []struct {
		name           string
		commits        []*git.Commit
		fallbackBranch string
		fillCommitBody bool
		expectedTitle  string
		expectedDesc   string
		wantErr        bool
	}{
		{
			name:           "No commits - use branch name",
			commits:        []*git.Commit{},
			fallbackBranch: "feature-branch",
			fillCommitBody: false,
			expectedTitle:  "feature branch",
			expectedDesc:   "",
			wantErr:        false,
		},
		{
			name: "Single commit - use commit title",
			commits: []*git.Commit{
				{Title: "Fix login bug", Sha: "abc123"},
			},
			fallbackBranch: "fix-login",
			fillCommitBody: false,
			expectedTitle:  "Fix login bug",
			expectedDesc:   "Fix login bug", // Falls back to title when fillCommitBody is false
			wantErr:        false,
		},
		{
			name: "Multiple commits without body - list format",
			commits: []*git.Commit{
				{Title: "Add feature A", Sha: "abc123"},
				{Title: "Fix bug B", Sha: "def456"},
			},
			fallbackBranch: "feature-branch",
			fillCommitBody: false,
			expectedTitle:  "feature branch",
			expectedDesc:   "- Fix bug B  \n- Add feature A  \n",
			wantErr:        false,
		},
		{
			name: "Multiple commits with body - list format",
			commits: []*git.Commit{
				{Title: "Add feature A", Sha: "abc123"},
				{Title: "Fix bug B", Sha: "def456"},
			},
			fallbackBranch: "feature-branch",
			fillCommitBody: true,
			expectedTitle:  "feature branch",
			// When git.CommitBody fails, we just get the titles
			expectedDesc: "",
			wantErr:      true, // This will error when trying to get commit bodies
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, desc, err := GenerateMRTitleAndBody(tt.commits, tt.fallbackBranch, tt.fillCommitBody)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedTitle, title)
			assert.Equal(t, tt.expectedDesc, desc)
		})
	}
}

func TestGenerateMRCommitListBody(t *testing.T) {
	tests := []struct {
		name           string
		commits        []*git.Commit
		fillCommitBody bool
		expectedOutput string
		wantErr        bool
	}{
		{
			name:           "Empty commits list",
			commits:        []*git.Commit{},
			fillCommitBody: false,
			expectedOutput: "",
			wantErr:        false,
		},
		{
			name: "Single commit without body",
			commits: []*git.Commit{
				{Title: "Fix login issue", Sha: "abc123"},
			},
			fillCommitBody: false,
			expectedOutput: "- Fix login issue  \n",
			wantErr:        false,
		},
		{
			name: "Multiple commits without body",
			commits: []*git.Commit{
				{Title: "First commit", Sha: "abc123"},
				{Title: "Second commit", Sha: "def456"},
				{Title: "Third commit", Sha: "ghi789"},
			},
			fillCommitBody: false,
			expectedOutput: "- Third commit  \n- Second commit  \n- First commit  \n",
			wantErr:        false,
		},
		{
			name: "Multiple commits with body enabled but git fails",
			commits: []*git.Commit{
				{Title: "First commit", Sha: "abc123"},
				{Title: "Second commit", Sha: "def456"},
			},
			fillCommitBody: true,
			// When git.CommitBody fails with fake SHAs, expect error
			expectedOutput: "",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := GenerateMRCommitListBody(tt.commits, tt.fillCommitBody)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedOutput, output)
		})
	}
}

func Test_MRCheckErrors(t *testing.T) {
	testCases := []struct {
		name    string
		mr      *gitlab.MergeRequest
		errOpts MRCheckErrOptions
		output  string
	}{
		{
			name: "draft",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:   1,
					Draft: true,
				},
			},
			errOpts: MRCheckErrOptions{
				Draft: true,
			},
			output: "this merge request is still a draft; run `glab mr update 1 --ready` to mark it as ready for review",
		},
		{
			name: "pipeline",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:                       1,
					MergeWhenPipelineSucceeds: true,
				},
				Pipeline: &gitlab.PipelineInfo{
					Status: "failure",
				},
			},
			errOpts: MRCheckErrOptions{
				PipelineStatus: true,
			},
			output: "the pipeline for this merge request has failed; it must succeed before merging",
		},
		{
			name: "merged",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:   1,
					State: "merged",
				},
			},
			errOpts: MRCheckErrOptions{
				Merged: true,
			},
			output: "this merge request has already been merged",
		},
		{
			name: "closed",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:   1,
					State: "closed",
				},
			},
			errOpts: MRCheckErrOptions{
				Closed: true,
			},
			output: "this merge request has been closed",
		},
		{
			name: "opened",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:   1,
					State: "opened",
				},
			},
			errOpts: MRCheckErrOptions{
				Opened: true,
			},
			output: "this merge request is already open",
		},
		{
			name: "subscribed",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID: 1,
				},
				Subscribed: true,
			},
			errOpts: MRCheckErrOptions{
				Subscribed: true,
			},
			output: "you are already subscribed to this merge request",
		},
		{
			name: "unsubscribed",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID: 1,
				},
				Subscribed: false,
			},
			errOpts: MRCheckErrOptions{
				Unsubscribed: true,
			},
			output: "you are not subscribed to this merge request",
		},
		{
			name: "merge-privilege",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID: 1,
				},
				User: struct {
					CanMerge bool "json:\"can_merge\""
				}{CanMerge: false},
			},
			errOpts: MRCheckErrOptions{
				MergePrivilege: true,
			},
			output: "you do not have permission to merge this merge request",
		},
		{
			name: "conflicts",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:          1,
					HasConflicts: true,
				},
			},
			errOpts: MRCheckErrOptions{
				Conflict: true,
			},
			output: "merge conflicts exist; resolve the conflicts and try again, or merge locally",
		},
		{
			name: "ci_must_pass with PipelineStatus",
			mr: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:                 1,
					DetailedMergeStatus: "ci_must_pass",
				},
			},
			errOpts: MRCheckErrOptions{
				PipelineStatus: true,
			},
			output: "this merge request requires a passing pipeline before merging",
		},
	}

	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			err := MRCheckErrors(tC.mr, tC.errOpts)
			assert.EqualError(t, err, tC.output)
		})
	}

	t.Run("ci_must_pass without PipelineStatus does not error", func(t *testing.T) {
		err := MRCheckErrors(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				IID:                 1,
				DetailedMergeStatus: "ci_must_pass",
			},
		}, MRCheckErrOptions{
			PipelineStatus: false,
		})
		assert.NoError(t, err)
	})

	t.Run("nil", func(t *testing.T) {
		err := MRCheckErrors(&gitlab.MergeRequest{}, MRCheckErrOptions{})
		assert.NoError(t, err)
	})
}

func Test_getMRForBranchFails(t *testing.T) {
	baseRepo := glrepo.NewWithHost("foo", "bar", "gitlab.com")

	t.Run("API-call-failed", func(t *testing.T) {
		api.ListMRs = func(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, listOpts ...api.CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
			return nil, errors.New("API call failed")
		}

		got, err := GetMRForBranch(t.Context(), nil, &gitlab.Client{}, MrOptions{baseRepo, "foo", "opened", true})
		assert.Nil(t, got)
		assert.EqualError(t, err, `failed to get open merge request for "foo": API call failed`)
	})

	t.Run("no-return", func(t *testing.T) {
		api.ListMRs = func(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, listOpts ...api.CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
			return []*gitlab.BasicMergeRequest{}, nil
		}

		got, err := GetMRForBranch(t.Context(), nil, &gitlab.Client{}, MrOptions{baseRepo, "foo", "opened", true})
		assert.Nil(t, got)
		assert.EqualError(t, err, `no open merge request available for "foo"`)
	})

	t.Run("owner-no-match", func(t *testing.T) {
		api.ListMRs = func(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, listOpts ...api.CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
			return []*gitlab.BasicMergeRequest{
				{
					IID: 1,
					Author: &gitlab.BasicUser{
						Username: "profclems",
					},
				},
				{
					IID: 2,
					Author: &gitlab.BasicUser{
						Username: "maxice8",
					},
				},
			}, nil
		}

		got, err := GetMRForBranch(t.Context(), nil, &gitlab.Client{}, MrOptions{baseRepo, "zemzale:foo", "opened", true})
		assert.Nil(t, got)
		assert.EqualError(t, err, `no open merge request available for "foo" owned by @zemzale`)
	})
}

func Test_getMRForBranch(t *testing.T) {
	baseRepo := glrepo.NewWithHost("foo", "bar", "gitlab.com")

	testCases := []struct {
		name   string
		input  string
		mrs    []*gitlab.BasicMergeRequest
		expect *gitlab.MergeRequest
	}{
		{
			name: "one-match",
			mrs: []*gitlab.BasicMergeRequest{
				{
					IID: 1,
					Author: &gitlab.BasicUser{
						Username: "profclems",
					},
				},
			},
			expect: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID: 1,
					Author: &gitlab.BasicUser{
						Username: "profclems",
					},
				},
			},
		},
		{
			name:  "owner-match",
			input: "maxice8:foo",
			mrs: []*gitlab.BasicMergeRequest{
				{
					IID: 1,
					Author: &gitlab.BasicUser{
						Username: "profclems",
					},
				},
				{
					IID: 2,
					Author: &gitlab.BasicUser{
						Username: "maxice8",
					},
				},
			},
			expect: &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID: 2,
					Author: &gitlab.BasicUser{
						Username: "maxice8",
					},
				},
			},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			api.ListMRs = func(_ *gitlab.Client, _ any, _ *gitlab.ListProjectMergeRequestsOptions, listOpts ...api.CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
				return tC.mrs, nil
			}

			got, err := GetMRForBranch(t.Context(), nil, &gitlab.Client{}, MrOptions{baseRepo, tC.input, "opened", true})
			require.NoError(t, err)

			assert.Equal(t, tC.expect.IID, got.IID)
			assert.Equal(t, tC.expect.Author.Username, got.Author.Username)
		})
	}
}

func Test_getMRForBranchPrompt(t *testing.T) {
	baseRepo := glrepo.NewWithHost("foo", "bar", "gitlab.com")

	api.ListMRs = func(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, listOpts ...api.CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
		return []*gitlab.BasicMergeRequest{
			{
				IID: 1,
				Author: &gitlab.BasicUser{
					Username: "profclems",
				},
			},
			{
				IID: 2,
				Author: &gitlab.BasicUser{
					Username: "maxice8",
				},
			},
		}, nil
	}

	c := ugh.New(t)
	c.Expect(ugh.Select(multipleMRSelectQuestion)).
		Do(ugh.SelectIndex(0))

	ios, cleanup := cmdtest.TestIOStreamsWithConsole(t, c)
	t.Cleanup(cleanup)

	got, err := GetMRForBranch(t.Context(), ios, &gitlab.Client{}, MrOptions{baseRepo, "foo", "opened", true})
	require.NoError(t, err)

	assert.Equal(t, int64(1), got.IID)
	assert.Equal(t, "profclems", got.Author.Username)
}

func Test_MRFromArgsWithOpts(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	ios.SetPrompt("false")

	// Create base factory for tests
	baseFactory := cmdtest.NewTestFactory(ios,
		cmdtest.WithBaseRepo("foo", "bar", ""),
		cmdtest.WithBranch("main"),
	)

	t.Run("success", func(t *testing.T) {
		t.Run("via-ID", func(t *testing.T) {
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				return &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:          2,
						Title:        "test mr",
						SourceBranch: "main",
					},
				}, nil
			}

			expectedRepo, err := baseFactory.BaseRepo()
			if err != nil {
				t.Skipf("failed to get base repo: %s", err)
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"2"}, "")
			require.NoError(t, err)

			assert.Equal(t, expectedRepo.FullName(), gotRepo.FullName())

			assert.Equal(t, int64(2), gotMR.IID)
			assert.Equal(t, "test mr", gotMR.Title)
			assert.Equal(t, "main", gotMR.SourceBranch)
		})
		t.Run("via-name", func(t *testing.T) {
			GetMRForBranch = func(_ context.Context, _ *iostreams.IOStreams, apiClient *gitlab.Client, mrOpts MrOptions) (*gitlab.BasicMergeRequest, error) {
				return &gitlab.BasicMergeRequest{
					IID:          2,
					Title:        "test mr",
					SourceBranch: "main",
				}, nil
			}

			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				return &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:          2,
						Title:        "test mr",
						SourceBranch: "main",
					},
				}, nil
			}

			expectedRepo, err := baseFactory.BaseRepo()
			if err != nil {
				t.Skipf("failed to get base repo: %s", err)
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"foo"}, "")
			require.NoError(t, err)

			assert.Equal(t, expectedRepo.FullName(), gotRepo.FullName())

			assert.Equal(t, int64(2), gotMR.IID)
			assert.Equal(t, "test mr", gotMR.Title)
			assert.Equal(t, "main", gotMR.SourceBranch)
		})
		t.Run("via-URL", func(t *testing.T) {
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				// Verify the correct MR ID from the URL is being used
				assert.Equal(t, int64(1234), mrID)
				return &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:          1234,
						Title:        "test mr from URL",
						SourceBranch: "feature-branch",
					},
				}, nil
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"https://gitlab.com/gitlab-org/cli/-/merge_requests/1234"}, "")
			require.NoError(t, err)

			// The repository should be extracted from the URL
			assert.Equal(t, "gitlab-org/cli", gotRepo.FullName())

			assert.Equal(t, int64(1234), gotMR.IID)
			assert.Equal(t, "test mr from URL", gotMR.Title)
			assert.Equal(t, "feature-branch", gotMR.SourceBranch)
		})
		t.Run("via-URL-self-managed", func(t *testing.T) {
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				// Verify the correct MR ID from the URL is being used
				assert.Equal(t, int64(42), mrID)
				return &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:          42,
						Title:        "test mr from self-managed",
						SourceBranch: "feature-branch",
					},
				}, nil
			}

			// Need to create a factory that can handle different hosts
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithBaseRepo("kde", "krita", "invent.kde.org"),
				cmdtest.WithBranch("main"),
			)

			gotMR, gotRepo, err := MRFromArgs(t.Context(), f, []string{"https://invent.kde.org/kde/krita/-/merge_requests/42"}, "")
			require.NoError(t, err)

			// The repository should be extracted from the URL
			assert.Equal(t, "kde/krita", gotRepo.FullName())
			assert.Equal(t, "invent.kde.org", gotRepo.RepoHost())

			assert.Equal(t, int64(42), gotMR.IID)
			assert.Equal(t, "test mr from self-managed", gotMR.Title)
			assert.Equal(t, "feature-branch", gotMR.SourceBranch)
		})
		t.Run("via-URL-cross-instance", func(t *testing.T) {
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				// Verify the correct MR ID from the URL is being used
				assert.Equal(t, int64(42), mrID)
				return &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:          42,
						Title:        "test mr from cross instance",
						SourceBranch: "feature-branch",
					},
				}, nil
			}

			// Create a factory with gitlab.com as base, but test with invent.kde.org URL
			// This tests the cross-instance hostname comparison logic
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithBaseRepo("gitlab-org", "cli", "gitlab.com"),
				cmdtest.WithBranch("main"),
			)

			gotMR, gotRepo, err := MRFromArgs(t.Context(), f, []string{"https://invent.kde.org/kde/krita/-/merge_requests/42"}, "")
			require.NoError(t, err)

			// The repository should be extracted from the URL (different from base repo)
			assert.Equal(t, "kde/krita", gotRepo.FullName())
			assert.Equal(t, "invent.kde.org", gotRepo.RepoHost())

			assert.Equal(t, int64(42), gotMR.IID)
			assert.Equal(t, "test mr from cross instance", gotMR.Title)
			assert.Equal(t, "feature-branch", gotMR.SourceBranch)
		})
		t.Run("via-URL-cross-instance-api-client", func(t *testing.T) {
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				return &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:          42,
						Title:        "test mr api client",
						SourceBranch: "feature-branch",
					},
				}, nil
			}

			// Track which hostname was used for ApiClient call
			var capturedHostname string

			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithBaseRepo("gitlab-org", "cli", "gitlab.com"),
				cmdtest.WithBranch("main"),
			)

			// Override ApiClient to capture the hostname parameter
			originalApiClient := f.ApiClientStub
			f.ApiClientStub = func(repoHost string) (*api.Client, error) {
				capturedHostname = repoHost
				return originalApiClient(repoHost) // Call original implementation
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), f, []string{"https://custom.gitlab.com/user/repo/-/merge_requests/42"}, "")
			require.NoError(t, err)

			// Verify that ApiClient was called with the correct hostname from URL
			assert.Equal(t, "custom.gitlab.com", capturedHostname)
			assert.Equal(t, "user/repo", gotRepo.FullName())
			assert.Equal(t, "custom.gitlab.com", gotRepo.RepoHost())
			assert.Equal(t, int64(42), gotMR.IID)
		})
	})

	t.Run("fail", func(t *testing.T) {
		t.Run("HttpClient", func(t *testing.T) {
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClientError(errors.New("failed to create HttpClient")),
				cmdtest.WithBaseRepo("foo", "bar", ""),
				cmdtest.WithBranch("main"),
			)

			gotMR, gotRepo, err := MRFromArgs(t.Context(), f, []string{}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, "failed to create HttpClient")
		})
		t.Run("BaseRepo", func(t *testing.T) {
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithBaseRepoError(errors.New("failed to create glrepo.Interface")),
				cmdtest.WithBranch("main"),
			)

			gotMR, gotRepo, err := MRFromArgs(t.Context(), f, []string{}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, "failed to create glrepo.Interface")
		})
		t.Run("Branch", func(t *testing.T) {
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithBaseRepo("foo", "bar", ""),
				cmdtest.WithBranchError(errors.New("failed to get branch")),
			)

			gotMR, gotRepo, err := MRFromArgs(t.Context(), f, []string{}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, "failed to get branch")
		})
		t.Run("Invalid-MR-ID", func(t *testing.T) {
			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"0"}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, "invalid merge request ID provided")
		})
		t.Run("invalid-name", func(t *testing.T) {
			GetMRForBranch = func(_ context.Context, _ *iostreams.IOStreams, _ *gitlab.Client, mrOpts MrOptions) (*gitlab.BasicMergeRequest, error) {
				return nil, fmt.Errorf("no merge requests from branch %q", mrOpts.Branch)
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"foo"}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, `no merge requests from branch "foo"`)
		})
		t.Run("api.GetMR", func(t *testing.T) {
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				return nil, errors.New("API call failed")
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"2"}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, "failed to get merge request 2: API call failed")
		})
		t.Run("URL-with-API-failure", func(t *testing.T) {
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				// This should be called with the ID from the URL (1234)
				assert.Equal(t, int64(1234), mrID)
				return nil, errors.New("API call failed for URL MR")
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"https://gitlab.com/gitlab-org/cli/-/merge_requests/1234"}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, "failed to get merge request 1234: API call failed for URL MR")
		})
		t.Run("URL-without-scheme", func(t *testing.T) {
			// Test that a URL without a scheme (http/https) fails to parse and falls back to branch lookup
			GetMRForBranch = func(_ context.Context, _ *iostreams.IOStreams, _ *gitlab.Client, mrOpts MrOptions) (*gitlab.BasicMergeRequest, error) {
				return nil, fmt.Errorf("no merge requests from branch %q", mrOpts.Branch)
			}

			gotMR, gotRepo, err := MRFromArgs(t.Context(), baseFactory, []string{"gitlab.com/gitlab-org/cli/-/merge_requests/1234"}, "")
			assert.Nil(t, gotMR)
			assert.Nil(t, gotRepo)
			assert.EqualError(t, err, `no merge requests from branch "gitlab.com/gitlab-org/cli/-/merge_requests/1234"`)
		})
	})
}

func Test_DisplayAllMRs(t *testing.T) {
	streams, _, _, _ := cmdtest.TestIOStreams()
	mrs := []*gitlab.BasicMergeRequest{
		{
			IID:          1,
			State:        "opened",
			Title:        "add tests",
			TargetBranch: "main",
			SourceBranch: "new-tests",
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/merge_requests/1",
				Relative: "#1",
				Short:    "#1",
			},
		},
		{
			IID:          2,
			State:        "merged",
			Title:        "fix bug",
			TargetBranch: "main",
			SourceBranch: "new-feature",
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/merge_requests/2",
				Relative: "#2",
				Short:    "#2",
			},
		},
		{
			IID:          1,
			State:        "closed",
			Title:        "add new feature",
			TargetBranch: "main",
			SourceBranch: "new-tests",
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/merge_requests/3",
				Relative: "#3",
				Short:    "#3",
			},
		},
	}

	expected := `!1	OWNER/REPO/merge_requests/1	add tests	(main) ← (new-tests)
!2	OWNER/REPO/merge_requests/2	fix bug	(main) ← (new-feature)
!1	OWNER/REPO/merge_requests/3	add new feature	(main) ← (new-tests)
`

	got := DisplayAllMRs(streams, mrs)
	assert.Equal(t, expected, got)
}

func Test_PrintMRApprovalState(t *testing.T) {
	scenarios := []struct {
		name          string
		approvalState *gitlab.MergeRequestApprovalState
		expected      string
	}{
		{
			name: "approved, min approvers met",
			approvalState: &gitlab.MergeRequestApprovalState{
				Rules: []*gitlab.MergeRequestApprovalRule{
					{
						ID:                1,
						Name:              "rule 1",
						ApprovalsRequired: 2,
						ApprovedBy: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "user1",
								Name:     "User One",
							},
							{
								ID:       2,
								Username: "user2",
								Name:     "User Two",
							},
						},
						Approved: true,
					},
				},
			},
			expected: `Rule "rule 1" sufficient approvals (2/2 required):
Name	Username	Approved
User One	user1	👍	
User Two	user2	👍	

`,
		},
		{
			name: "not-approved, min approvers not met",
			approvalState: &gitlab.MergeRequestApprovalState{
				Rules: []*gitlab.MergeRequestApprovalRule{
					{
						ID:                1,
						Name:              "rule 1",
						ApprovalsRequired: 2,
						ApprovedBy: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "user1",
								Name:     "User One",
							},
						},
						Approved: false,
					},
				},
			},
			expected: `Rule "rule 1" insufficient approvals (1/2 required):
Name	Username	Approved
User One	user1	👍	

`,
		},
		{
			name: "approved, eligible approvers",
			approvalState: &gitlab.MergeRequestApprovalState{
				Rules: []*gitlab.MergeRequestApprovalRule{
					{
						ID:                1,
						Name:              "rule 1",
						ApprovalsRequired: 2,
						EligibleApprovers: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "user1",
								Name:     "User One",
							},
							{
								ID:       2,
								Username: "user2",
								Name:     "User Two",
							},
						},
						ApprovedBy: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "user1",
								Name:     "User One",
							},
							{
								ID:       2,
								Username: "user2",
								Name:     "User Two",
							},
						},
						Approved: true,
					},
				},
			},
			expected: `Rule "rule 1" sufficient approvals (2/2 required):
Name	Username	Approved
User One	user1	👍	
User Two	user2	👍	

`,
		},
		{
			name: "not approved, missing eligible approver",
			approvalState: &gitlab.MergeRequestApprovalState{
				Rules: []*gitlab.MergeRequestApprovalRule{
					{
						ID:                1,
						Name:              "rule 1",
						ApprovalsRequired: 2,
						EligibleApprovers: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "user1",
								Name:     "User One",
							},
							{
								ID:       2,
								Username: "user2",
								Name:     "User Two",
							},
						},
						ApprovedBy: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "user1",
								Name:     "User One",
							},
						},
						Approved: false,
					},
				},
			},
			expected: `Rule "rule 1" insufficient approvals (1/2 required):
Name	Username	Approved
User One	user1	👍	
User Two	user2	-	

`,
		},
		{
			name: "deterministic output without eligible approvers",
			approvalState: &gitlab.MergeRequestApprovalState{
				Rules: []*gitlab.MergeRequestApprovalRule{
					{
						ID:                1,
						Name:              "rule 1",
						ApprovalsRequired: 2,
						ApprovedBy: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "aaa",
								Name:     "User One",
							},
							{
								ID:       2,
								Username: "zzz",
								Name:     "User Two",
							},
							{
								ID:       3,
								Username: "000",
								Name:     "User Three",
							},
							{
								ID:       4,
								Username: "xyz",
								Name:     "User Four",
							},
						},
						Approved: true,
					},
				},
			},
			expected: `Rule "rule 1" sufficient approvals (4/2 required):
Name	Username	Approved
User Three	000	👍	
User One	aaa	👍	
User Four	xyz	👍	
User Two	zzz	👍	

`,
		},
		{
			name: "deterministic output with all eligible approvers",
			approvalState: &gitlab.MergeRequestApprovalState{
				Rules: []*gitlab.MergeRequestApprovalRule{
					{
						ID:                1,
						Name:              "rule 1",
						ApprovalsRequired: 2,
						ApprovedBy: []*gitlab.BasicUser{
							{
								ID:       4,
								Username: "xyz",
								Name:     "User Four",
							},
							{
								ID:       1,
								Username: "aaa",
								Name:     "User One",
							},
							{
								ID:       2,
								Username: "zzz",
								Name:     "User Two",
							},
							{
								ID:       3,
								Username: "000",
								Name:     "User Three",
							},
						},
						EligibleApprovers: []*gitlab.BasicUser{
							{
								ID:       4,
								Username: "xyz",
								Name:     "User Four",
							},
							{
								ID:       2,
								Username: "zzz",
								Name:     "User Two",
							},
						},
						Approved: true,
					},
				},
			},
			expected: `Rule "rule 1" sufficient approvals (4/2 required):
Name	Username	Approved
User Four	xyz	👍	
User Two	zzz	👍	
User Three	000	👍	
User One	aaa	👍	

`,
		},
		{
			name: "deterministic output with no eligible approvers",
			approvalState: &gitlab.MergeRequestApprovalState{
				Rules: []*gitlab.MergeRequestApprovalRule{
					{
						ID:                1,
						Name:              "rule 1",
						ApprovalsRequired: 2,
						ApprovedBy: []*gitlab.BasicUser{
							{
								ID:       1,
								Username: "aaa",
								Name:     "User One",
							},
							{
								ID:       3,
								Username: "000",
								Name:     "User Three",
							},
						},
						EligibleApprovers: []*gitlab.BasicUser{
							{
								ID:       4,
								Username: "xyz",
								Name:     "User Four",
							},
							{
								ID:       2,
								Username: "zzz",
								Name:     "User Two",
							},
						},
						Approved: true,
					},
				},
			},
			expected: `Rule "rule 1" sufficient approvals (2/2 required):
Name	Username	Approved
User Four	xyz	-	
User Two	zzz	-	
User Three	000	👍	
User One	aaa	👍	

`,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			streams, _, stdout, _ := cmdtest.TestIOStreams()

			PrintMRApprovalState(streams, scenario.approvalState)
			assert.Equal(t, scenario.expected, stdout.String())
		})
	}
}
