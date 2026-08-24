//go:build !integration

package ciutils

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~timofurrer/ugh"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDisplayMultiplePipelines_UsesSinglePipelineInfo(t *testing.T) {
	t.Parallel()

	pipelines := []*gitlab.PipelineInfo{
		{
			ID:     456,
			IID:    789,
			Status: "success",
			Ref:    "main",
			SHA:    "abc0def0",
			WebURL: "https://gitlab.com/project/-/pipelines/123",
		},
	}
	ios, _, _, _ := cmdtest.TestIOStreams()

	result := DisplayMultiplePipelines(ios, pipelines, "group/project")

	assert.Contains(t, result, "success")
	assert.Contains(t, result, "#456")
	assert.Contains(t, result, "#789")
	assert.Contains(t, result, "main")
}

func TestDisplayMultiplePipelines_UsesMultiplePipelinesInfo(t *testing.T) {
	t.Parallel()

	pipelines := []*gitlab.PipelineInfo{
		{
			ID:     900,
			Status: "success",
			Ref:    "main",
		},
		{
			ID:     901,
			Status: "failed",
			Ref:    "develop",
		},
		{
			ID:     902,
			Status: "running",
			Ref:    "feat/ure",
		},
	}
	ios, _, _, _ := cmdtest.TestIOStreams()

	result := DisplayMultiplePipelines(ios, pipelines, "group/project")

	assert.Contains(t, result, "success")
	assert.Contains(t, result, "failed")
	assert.Contains(t, result, "running")
}

func TestDisplayMultiplePipelines_HandlesEmptyPipelines(t *testing.T) {
	t.Parallel()

	pipelines := []*gitlab.PipelineInfo{}
	ios, _, _, _ := cmdtest.TestIOStreams()

	result := DisplayMultiplePipelines(ios, pipelines, "group/project")

	assert.Equal(t, "No Pipelines available on group/project", result)
}

func TestGetJobId(t *testing.T) {
	t.Parallel()
	// Response indicating last page
	lastPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 0,
	}

	// Response indicating there's a next page
	nextPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 2,
	}

	type testCase struct {
		name          string
		jobName       string
		pipelineId    int
		console       func(t *testing.T) *ugh.Console
		setupMock     func(tc *gitlabtesting.TestClient)
		expectedOut   int64
		expectedError string
	}

	tests := []testCase{
		{
			name:        "when getJobId with integer is requested",
			jobName:     "1122",
			expectedOut: 1122,
			setupMock:   func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:        "when getJobId with name and pipelineId is requested",
			jobName:     "lint",
			pipelineId:  123,
			expectedOut: 1122,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
		{
			name:        "when getJobId with name and pipelineId is requested and job is found on page 2",
			jobName:     "deploy",
			pipelineId:  123,
			expectedOut: 1144,
			setupMock: func(tc *gitlabtesting.TestClient) {
				// First page
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, nextPageResponse, nil)
				// Second page
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1133, Name: "test", Status: "failed"},
						{ID: 1144, Name: "deploy", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
		{
			name:          "when getJobId with name and pipelineId is requested and listJobs throws error",
			jobName:       "lint",
			pipelineId:    123,
			expectedError: "list pipeline jobs:",
			expectedOut:   0,
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:        "when getJobId with name and last pipeline is requested",
			jobName:     "lint",
			pipelineId:  0,
			expectedOut: 1122,
			setupMock: func(tc *gitlabtesting.TestClient) {
				// GetPipelineWithFallback first tries GetLatestPipeline
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(&gitlab.Pipeline{ID: 123}, nil, nil)
				// Then checks if pipeline has jobs
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1, Name: "test"},
					}, nil, nil)
				// Then GetJobId lists all jobs for the pipeline
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
		{
			name:          "when getJobId with name and last pipeline is requested and getCommits throws error",
			jobName:       "lint",
			pipelineId:    0,
			expectedError: "get pipeline: failed to get pipeline for branch main:",
			expectedOut:   0,
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				// GetPipelineWithFallback tries GetLatestPipeline
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
				// Then tries MR lookup (will also fail)
				tc.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:          "when getJobId with name and last pipeline is requested and getJobs throws error",
			jobName:       "lint",
			pipelineId:    0,
			expectedError: "list pipeline jobs:",
			expectedOut:   0,
			setupMock: func(tc *gitlabtesting.TestClient) {
				// GetPipelineWithFallback tries GetLatestPipeline
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(&gitlab.Pipeline{ID: 123}, nil, nil)
				// Check if pipeline has jobs
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any()).
					Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil)
				// Then GetJobId tries to list all jobs (this fails)
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:        "when getJobId with pipelineId is requested, ask for job and answer",
			jobName:     "",
			pipelineId:  123,
			expectedOut: 1122,
			console: func(t *testing.T) *ugh.Console {
				t.Helper()

				c := ugh.New(t)
				c.Expect(ugh.Select("Select pipeline job to trace:")).
					Do(ugh.SelectIndex(0))
				return c
			},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			factoryOpts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("main"),
			}

			var ios *iostreams.IOStreams
			if tc.console != nil {
				var cleanup func()
				ios, cleanup = cmdtest.TestIOStreamsWithConsole(t, tc.console(t))
				t.Cleanup(cleanup)
			} else {
				ios, _, _, _ = cmdtest.TestIOStreams()
			}
			f := cmdtest.NewTestFactory(ios, factoryOpts...)

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()

			output, err := GetJobId(t.Context(), &JobInputs{
				JobName:    tc.jobName,
				PipelineId: tc.pipelineId,
				Branch:     "main",
			}, &JobOptions{
				IO:     f.IO(),
				Repo:   repo,
				Client: client,
			})

			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			}
			assert.Equal(t, tc.expectedOut, output)
		})
	}
}

func TestGetJobIdInteractivePaginates(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	nextPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 2,
	}
	lastPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
	}
	var requestedPages []string
	testClient.MockJobs.EXPECT().
		ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Times(2).
		DoAndReturn(func(_ any, _ int64, _ *gitlab.ListJobsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Job, *gitlab.Response, error) {
			req, err := retryablehttp.NewRequest(http.MethodGet, "https://gitlab.com", nil)
			require.NoError(t, err)
			for _, option := range options {
				if option != nil {
					require.NoError(t, option(req))
				}
			}

			requestedPages = append(requestedPages, req.URL.Query().Get("page"))
			if len(requestedPages) == 1 {
				return []*gitlab.Job{{ID: 1122, Name: "lint", Status: "failed"}}, nextPageResponse, nil
			}
			return []*gitlab.Job{{ID: 1144, Name: "deploy", Status: "success"}}, lastPageResponse, nil
		})

	console := ugh.New(t)
	console.Expect(ugh.Select("Select pipeline job to trace:")).
		Do(ugh.SelectIndex(1))
	ios, cleanup := cmdtest.TestIOStreamsWithConsole(t, console)
	t.Cleanup(cleanup)
	f := cmdtest.NewTestFactory(ios,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBranch("main"),
	)
	client, err := f.GitLabClient()
	require.NoError(t, err)
	repo, err := f.BaseRepo()
	require.NoError(t, err)

	jobID, err := GetJobId(t.Context(), &JobInputs{
		PipelineId: 123,
		Branch:     "main",
	}, &JobOptions{
		IO:     f.IO(),
		Repo:   repo,
		Client: client,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(1144), jobID)
	assert.Equal(t, []string{"", "2"}, requestedPages)
}

func TestParseCSVToIntSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expectedOut []int
	}{
		{
			name:        "when input is empty",
			input:       "",
			expectedOut: nil,
		},
		{
			name:        "when input is a comma-separated string",
			input:       "111,222,333",
			expectedOut: []int{111, 222, 333},
		},
		{
			name:        "when input is a space-separated string",
			input:       "111 222 333 4444",
			expectedOut: []int{111, 222, 333, 4444},
		},
		{
			name:        "when input is a space-separated and comma-separated string",
			input:       "111, 222, 333, 4444",
			expectedOut: []int{111, 222, 333, 4444},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Simulate splitting raw input
			args := strings.Fields(tc.input)

			output, err := IDsFromArgs(args)
			if err != nil {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.expectedOut, output)
		})
	}
}

func TestTraceJob(t *testing.T) {
	t.Parallel()
	type testCase struct {
		name          string
		jobName       string
		pipelineId    int
		setupMock     func(tc *gitlabtesting.TestClient)
		expectedError string
	}

	tests := []testCase{
		{
			name:    "when traceJob is requested",
			jobName: "1122",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(&gitlab.Job{
						ID:     1122,
						Name:   "lint",
						Status: "success",
					}, nil, nil)

				tc.MockJobs.EXPECT().
					GetTraceFile("OWNER/REPO", int64(1122), gomock.Any()).
					Return(bytes.NewReader([]byte("Lorem ipsum")), nil, nil)
			},
		},
		{
			name:    "when the job was canceled",
			jobName: "1122",
			setupMock: func(tc *gitlabtesting.TestClient) {
				// The API reports this state as "canceled". A single GetJob call
				// is expected: the trace loop has to stop once it sees it.
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(&gitlab.Job{
						ID:     1122,
						Name:   "lint",
						Status: "canceled",
					}, nil, nil)

				tc.MockJobs.EXPECT().
					GetTraceFile("OWNER/REPO", int64(1122), gomock.Any()).
					Return(bytes.NewReader([]byte("Job canceled")), nil, nil)
			},
		},
		{
			name:          "when traceJob is requested and getJob throws error",
			jobName:       "1122",
			expectedError: "failed to find job:",
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:          "when traceJob is requested and trace job throws error",
			jobName:       "1122",
			expectedError: "failed to find job:",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(&gitlab.Job{
						ID:     1122,
						Name:   "lint",
						Status: "success",
					}, nil, nil)

				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					GetTraceFile("OWNER/REPO", int64(1122), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			ios, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("main"),
			)

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()

			err := TraceJob(t.Context(), &JobInputs{
				JobName:    tc.jobName,
				PipelineId: tc.pipelineId,
				Branch:     "main",
			}, &JobOptions{
				IO:           f.IO(),
				Repo:         repo,
				Client:       client,
				PollInterval: time.Millisecond,
			})

			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			}
		})
	}
}

func TestRunTraceJob(t *testing.T) {
	t.Parallel()

	t.Run("traces the requested job ID in a string-identified project", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockJobs.EXPECT().
			GetJob("OWNER/REPO", int64(4242)).
			Return(&gitlab.Job{ID: 4242, Name: "lint", Status: "success"}, nil, nil)
		tc.MockJobs.EXPECT().
			GetTraceFile("OWNER/REPO", int64(4242), gomock.Any()).
			Return(bytes.NewReader([]byte("parent trace")), nil, nil)

		var out bytes.Buffer
		err := RunTraceJob(t.Context(), tc.Client, &out, "OWNER/REPO", 4242, time.Millisecond)

		require.NoError(t, err)
		assert.Contains(t, out.String(), "parent trace")
	})

	t.Run("traces a job in a numerically-identified downstream project", func(t *testing.T) {
		t.Parallel()

		// A child pipeline's project arrives as a numeric ID, not "owner/repo".
		tc := gitlabtesting.NewTestClient(t)
		tc.MockJobs.EXPECT().
			GetJob(int64(777), int64(99)).
			Return(&gitlab.Job{ID: 99, Name: "lint", Status: "success"}, nil, nil)
		tc.MockJobs.EXPECT().
			GetTraceFile(int64(777), int64(99), gomock.Any()).
			Return(bytes.NewReader([]byte("child trace")), nil, nil)

		var out bytes.Buffer
		err := RunTraceJob(t.Context(), tc.Client, &out, int64(777), 99, time.Millisecond)

		require.NoError(t, err)
		assert.Contains(t, out.String(), "child trace")
	})

	t.Run("returns the error when the job cannot be fetched", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		notFound := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
		tc.MockJobs.EXPECT().
			GetJob(int64(777), int64(99)).
			Return(nil, notFound, assert.AnError)

		var out bytes.Buffer
		err := RunTraceJob(t.Context(), tc.Client, &out, int64(777), 99, time.Millisecond)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to find job:")
	})
}

// TestGetDefaultBranch_HappyPath tests successful scenarios for GetDefaultBranch
func TestGetDefaultBranch_HappyPath(t *testing.T) {
	tests := []struct {
		name           string
		defaultBranch  string
		expectedResult string
	}{
		{
			name:           "when API returns default branch",
			defaultBranch:  "develop",
			expectedResult: "develop",
		},
		{
			name:           "when API returns main as default branch",
			defaultBranch:  "main",
			expectedResult: "main",
		},
		{
			name:           "when API returns empty default branch",
			defaultBranch:  "",
			expectedResult: "main",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			project := &gitlab.Project{
				DefaultBranch: tc.defaultBranch,
			}
			testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(project, nil, nil)

			ios, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()
			result := GetDefaultBranch(repo, client)
			require.Equal(t, tc.expectedResult, result)
		})
	}
}

// TestGetDefaultBranch_ErrorCases tests error scenarios for GetDefaultBranch
func TestGetDefaultBranch_ErrorCases(t *testing.T) {
	tests := []struct {
		name           string
		factoryOptions []cmdtest.FactoryOption
		expectMain     bool
	}{
		{
			name:           "when BaseRepo fails",
			factoryOptions: []cmdtest.FactoryOption{cmdtest.WithBaseRepoError(assert.AnError)},
			expectMain:     true,
		},
		{
			name:           "when GitLabClient fails",
			factoryOptions: []cmdtest.FactoryOption{cmdtest.WithGitLabClientError(assert.AnError)},
			expectMain:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ios, _, _, _ := cmdtest.TestIOStreams()
			factory := cmdtest.NewTestFactory(ios, tc.factoryOptions...)

			client, _ := factory.GitLabClient()
			repo, _ := factory.BaseRepo()

			// Run the test with the configured factory
			result := GetDefaultBranch(repo, client)
			require.Equal(t, "main", result)
		})
	}
}

// TestGetDefaultBranch_APIErrorCases tests API failure scenarios for GetDefaultBranch
func TestGetDefaultBranch_APIErrorCases(t *testing.T) {
	t.Run("when API call fails", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(nil, nil, assert.AnError)

		ios, _, _, _ := cmdtest.TestIOStreams()
		f := cmdtest.NewTestFactory(ios,
			cmdtest.WithGitLabClient(testClient.Client),
		)

		client, _ := f.GitLabClient()
		repo, _ := f.BaseRepo()
		result := GetDefaultBranch(repo, client)
		require.Equal(t, "main", result)
	})
}

// TestGetBranch_HappyPath tests successful scenarios for GetBranch
func TestGetBranch_HappyPath(t *testing.T) {
	tests := []struct {
		name             string
		specifiedBranch  string
		gitBranch        string
		apiDefaultBranch string
		expectedResult   string
	}{
		{
			name:            "when branch is specified",
			specifiedBranch: "feature-branch",
			expectedResult:  "feature-branch",
		},
		{
			name:           "when no branch specified and git works",
			gitBranch:      "current-git-branch",
			expectedResult: "current-git-branch",
		},
		{
			name:             "when no branch specified and git fails, uses API default",
			apiDefaultBranch: "develop",
			expectedResult:   "develop",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			if tc.apiDefaultBranch != "" {
				project := &gitlab.Project{
					DefaultBranch: tc.apiDefaultBranch,
				}
				testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(project, nil, nil)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			if tc.gitBranch != "" {
				f.BranchStub = func() (string, error) {
					return tc.gitBranch, nil
				}
			} else if tc.apiDefaultBranch != "" {
				f.BranchStub = func() (string, error) {
					return "", assert.AnError
				}
			}

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()
			result := GetBranch(tc.specifiedBranch, f.BranchStub, repo, client)
			require.Equal(t, tc.expectedResult, result)
		})
	}
}

// TestGetBranch_ErrorFallback tests error scenarios that fallback to main
func TestGetBranch_ErrorFallback(t *testing.T) {
	t.Run("when no branch specified and git fails, falls back to main", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock API failure
		testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(nil, nil, assert.AnError)

		ios, _, _, _ := cmdtest.TestIOStreams()
		f := cmdtest.NewTestFactory(ios,
			cmdtest.WithGitLabClient(testClient.Client),
		)

		f.BranchStub = func() (string, error) {
			return "", assert.AnError
		}

		client, _ := f.GitLabClient()
		repo, _ := f.BaseRepo()
		result := GetBranch("", f.BranchStub, repo, client)
		require.Equal(t, "main", result)
	})
}
