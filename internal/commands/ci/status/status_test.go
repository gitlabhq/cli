//go:build !integration

package status

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_getPipelineWithFallback(t *testing.T) {
	tests := []struct {
		name           string
		setupMocks     func(*gitlabtesting.TestClient)
		branch         string
		wantPipeline   *gitlab.Pipeline
		wantErr        bool
		expectedErrMsg string
	}{
		{
			name:   "successfully gets latest pipeline",
			branch: "main",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
					Return(&gitlab.Pipeline{ID: 1, Status: "success"}, nil, nil)

				// Mock job check to verify pipeline has jobs
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil)
			},
			wantPipeline: &gitlab.Pipeline{ID: 1, Status: "success"},
			wantErr:      false,
		},
		{
			name:   "falls back to MR pipeline when branch pipeline has no jobs",
			branch: "feature",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				// Latest pipeline found but has no jobs (e.g., external pipeline)
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("feature")}, gomock.Any()).
					Return(&gitlab.Pipeline{ID: 1, Status: "success"}, nil, nil)

				// Mock job check returns empty list
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{}, nil, nil)

				// Find and get MR
				tc.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.BasicMergeRequest{{IID: 1}}, nil, nil)

				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{IID: 1},
						HeadPipeline:      &gitlab.Pipeline{ID: 2, Status: "running"},
					}, nil, nil)

				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Pipeline{
						ID:     2,
						Status: "running",
					}, nil, nil)
			},
			wantPipeline: &gitlab.Pipeline{ID: 2, Status: "running"},
			wantErr:      false,
		},
		{
			name:   "falls back to MR pipeline when latest not found",
			branch: "feature",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				// Latest pipeline not found
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("feature")}, gomock.Any()).
					Return(nil, nil, errors.New("not found"))

				// Find and get MR
				tc.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.BasicMergeRequest{{IID: 1}}, nil, nil)

				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{IID: 1},
						HeadPipeline:      &gitlab.Pipeline{ID: 2, Status: "running"},
					}, nil, nil)

				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(2), gomock.Any()).
					Return(&gitlab.Pipeline{
						ID:     2,
						Status: "running",
					}, nil, nil)
			},
			wantPipeline: &gitlab.Pipeline{ID: 2, Status: "running"},
			wantErr:      false,
		},
		{
			name:   "returns error when no pipeline found",
			branch: "feature",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				// Latest pipeline not found
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("feature")}, gomock.Any()).
					Return(nil, nil, errors.New("not found"))

				// No MRs found
				tc.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.BasicMergeRequest{}, nil, nil)
			},
			wantPipeline:   nil,
			wantErr:        true,
			expectedErrMsg: "no pipeline found for branch feature and failed to find associated merge request",
		},
		{
			name:   "returns error when MR has no pipeline",
			branch: "feature",
			setupMocks: func(tc *gitlabtesting.TestClient) {
				// Latest pipeline not found
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("feature")}, gomock.Any()).
					Return(nil, nil, errors.New("not found"))

				// Find MR but no pipeline
				tc.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.BasicMergeRequest{{IID: 1}}, nil, nil)
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{IID: 1},
					}, nil, nil)
			},
			wantPipeline:   nil,
			wantErr:        true,
			expectedErrMsg: "no pipeline found. It might not exist yet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)
			tt.setupMocks(tc)

			// Create test IO streams
			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))

			pipeline, err := ciutils.GetPipelineWithFallback(t.Context(), tc.Client, "OWNER/REPO", tt.branch, ios)

			if tt.wantErr {
				require.Error(t, err)
				if tt.expectedErrMsg != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMsg)
				}
				assert.Nil(t, pipeline)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPipeline.ID, pipeline.ID)
			assert.Equal(t, tt.wantPipeline.Status, pipeline.Status)
		})
	}
}

func TestCiStatusCommand_NoPrompt(t *testing.T) {
	// Test that the command exits cleanly when NO_PROMPT is enabled
	// and doesn't hang waiting for user input
	tc := gitlabtesting.NewTestClient(t)

	// Mock calls in expected order
	gomock.InOrder(
		// Mock a finished pipeline so the command doesn't loop
		tc.MockPipelines.EXPECT().
			GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Pipeline{ID: 1, Status: "success"}, nil, nil),

		// Mock job check in GetPipelineWithFallback
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil),

		// Mock jobs for the pipeline - need to handle pagination
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Job{
				{ID: 1, Name: "test", Stage: "test", Status: "success"},
			}, &gitlab.Response{NextPage: 0}, nil),
	)

	exec := cmdtest.SetupCmdForTest(t, NewCmdStatus, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBranch("main"),
		// Create custom option to disable prompts
		func(f *cmdtest.Factory) {
			f.IOStub.SetPrompt("true")
		},
	)

	// This should complete without hanging
	_, err := exec("")
	require.NoError(t, err)
}

func TestCiStatusCommand_WithPromptsEnabled_FinishedPipeline(t *testing.T) {
	// Test that the command shows pipeline status and exits cleanly
	// when dealing with a finished pipeline (no interactive prompts needed)
	tc := gitlabtesting.NewTestClient(t)

	// Mock calls in expected order
	gomock.InOrder(
		// Mock a finished pipeline
		tc.MockPipelines.EXPECT().
			GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Pipeline{ID: 1, Status: "success"}, nil, nil),

		// Mock job check in GetPipelineWithFallback
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil),

		// Mock jobs for the pipeline - need to handle pagination
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Job{
				{ID: 1, Name: "test", Stage: "test", Status: "success"},
			}, &gitlab.Response{NextPage: 0}, nil),
	)

	exec := cmdtest.SetupCmdForTest(t, NewCmdStatus, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBranch("main"),
	)

	// This should complete without hanging since the pipeline is finished
	_, err := exec("")
	require.NoError(t, err)
}

func TestCiStatusCommand_JSONWithWait_Error(t *testing.T) {
	// Test that providing both `--output json` and `--wait`
	// returns the "--output json cannot be used with --live, --wait, or --compact flags" error.
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(t, NewCmdStatus, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBranch("main"),
	)

	_, err := exec("--output json --wait")
	require.Error(t, err)
	assert.EqualError(t, err, "--output json cannot be used with --live, --wait, or --compact flags")
}

func TestCiStatusCommand_Wait_NoPrompt(t *testing.T) {
	// Test that --wait disables prompting and exits cleanly on a finished pipeline.
	// This is the same as TestCiStatusCommand_NoPrompt, except using the --wait flag.
	tc := gitlabtesting.NewTestClient(t)

	gomock.InOrder(
		tc.MockPipelines.EXPECT().
			GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Pipeline{ID: 1, Status: "success"}, nil, nil),

		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil),

		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Job{
				{ID: 1, Name: "test", Stage: "test", Status: "success"},
			}, &gitlab.Response{NextPage: 0}, nil),
	)

	exec := cmdtest.SetupCmdForTest(t, NewCmdStatus, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBranch("main"),
	)

	// This should complete without hanging. Unlike TestCiStatusCommand_NoPrompt,
	// prompting is disabled via the --wait flag rather than f.IOStub.SetPrompt.
	_, err := exec("--wait")
	require.NoError(t, err)
}

func Test_isLivePollableStatus(t *testing.T) {
	t.Parallel()

	// Source of truth: gitlab.BuildStateValue constants in client-go (types.go).
	// "manual" is excluded because polling won't progress it; "canceled" is excluded
	// because canceled pipelines don't transition back to running on their own —
	// the canceled-supersede check in the live loop handles the rebase case.
	tests := []struct {
		status string
		want   bool
	}{
		{"created", true},
		{"waiting_for_resource", true},
		{"preparing", true},
		{"pending", true},
		{"running", true},
		{"scheduled", true},
		{"success", false},
		{"failed", false},
		{"canceled", false},
		{"skipped", false},
		{"manual", false},
		{"", false},
		{"unknown_future_status", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isLivePollableStatus(tt.status))
		})
	}
}

func TestCiStatusCommand_Live_CanceledSuperseded(t *testing.T) {
	// Intentionally not t.Parallel(): the live path enters uilive.New(), which
	// writes to package-level globals in github.com/gosuri/uilive that the race
	// detector flags when multiple tests run concurrently.

	// When --live encounters a canceled pipeline AND a newer pipeline exists
	// for the branch (e.g. rebase auto-canceled the previous run), switch to
	// the new pipeline instead of exiting.
	tc := gitlabtesting.NewTestClient(t)

	gomock.InOrder(
		// Initial fetch: returns canceled pipeline ID=1.
		tc.MockPipelines.EXPECT().
			GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Pipeline{ID: 1, Status: "canceled"}, nil, nil),
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil),

		// Loop iteration 1: list jobs for the canceled pipeline.
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Job{
				{ID: 1, Name: "test", Stage: "test", Status: "canceled"},
			}, &gitlab.Response{NextPage: 0}, nil),

		// Canceled-supersede check: a newer pipeline now exists for the branch.
		tc.MockPipelines.EXPECT().
			GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Pipeline{ID: 2, Status: "success"}, nil, nil),
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(2), gomock.Any()).
			Return([]*gitlab.Job{{ID: 2, Name: "test"}}, nil, nil),
	)

	exec := cmdtest.SetupCmdForTest(t, NewCmdStatus, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBranch("main"),
	)

	_, err := exec("--live")
	require.NoError(t, err)
	// gomock.InOrder above asserts that the canceled-supersede check re-queried
	// for the latest pipeline after seeing the canceled status.
}

func TestCiStatusCommand_Live_CanceledNoNewerPipeline(t *testing.T) {
	// Intentionally not t.Parallel(): see comment in
	// TestCiStatusCommand_Live_CanceledSuperseded.

	// When --live encounters a canceled pipeline and no newer pipeline exists
	// for the branch, exit cleanly instead of looping forever.
	tc := gitlabtesting.NewTestClient(t)

	gomock.InOrder(
		// Initial fetch: returns canceled pipeline ID=1.
		tc.MockPipelines.EXPECT().
			GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Pipeline{ID: 1, Status: "canceled"}, nil, nil),
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil),

		// Loop iteration 1: list jobs.
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Job{
				{ID: 1, Name: "test", Stage: "test", Status: "canceled"},
			}, &gitlab.Response{NextPage: 0}, nil),

		// Canceled-supersede check: same ID returned, so we exit.
		tc.MockPipelines.EXPECT().
			GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Pipeline{ID: 1, Status: "canceled"}, nil, nil),
		tc.MockJobs.EXPECT().
			ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil),
	)

	exec := cmdtest.SetupCmdForTest(t, NewCmdStatus, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBranch("main"),
	)

	_, err := exec("--live")
	require.NoError(t, err)
}

func TestCiStatus_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock a finished pipeline
	tc.MockPipelines.EXPECT().
		GetLatestPipeline("OWNER/REPO", &gitlab.GetLatestPipelineOptions{Ref: new("main")}, gomock.Any()).
		Return(&gitlab.Pipeline{ID: 1, Status: "success", Ref: "main"}, nil, nil)

	// Mock job check in GetPipelineWithFallback
	tc.MockJobs.EXPECT().
		ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any()).
		Return([]*gitlab.Job{{ID: 1, Name: "test"}}, nil, nil)

	// Mock jobs for the pipeline with pagination
	tc.MockJobs.EXPECT().
		ListPipelineJobs("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		Return([]*gitlab.Job{
			{ID: 1, Name: "test", Stage: "test", Status: "success"},
		}, &gitlab.Response{NextPage: 0}, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdStatus, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBranch("main"),
	)

	out, err := exec("--output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":1`)
	assert.Contains(t, out.String(), `"status":"success"`)
	assert.Contains(t, out.String(), `"jobs"`)
	assert.Empty(t, out.Stderr())
}
