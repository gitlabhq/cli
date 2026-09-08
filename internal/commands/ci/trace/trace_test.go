//go:build !integration

package trace

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCiTrace(t *testing.T) {
	t.Parallel()

	// Response indicating last page
	lastPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 0,
	}

	type testCase struct {
		name          string
		args          string
		expectedOut   string
		expectedError string
		setupMock     func(tc *gitlabtesting.TestClient)
	}

	tests := []testCase{
		{
			name:        "when trace for job-id is requested",
			args:        "1122",
			expectedOut: "\nGetting job trace...\nShowing logs for lint job #1122.\nLorem ipsum",
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
			name:          "when trace for job-id is requested and getTrace throws error",
			args:          "1122",
			expectedError: "failed to find job",
			expectedOut:   "\nGetting job trace...\nShowing logs for lint job #1122.\n",
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
					Return(nil, forbiddenResponse, fmt.Errorf("GET https://gitlab.com/api/v4/projects/OWNER%%2FREPO/jobs/1122/trace: 403"))
			},
		},
		{
			name:          "when trace for job-id is requested and getJob throws error",
			args:          "1122",
			expectedError: "failed to find job",
			expectedOut:   "\nGetting job trace...\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(nil, forbiddenResponse, fmt.Errorf("GET https://gitlab.com/api/v4/projects/OWNER%%2FREPO/jobs/1122: 403"))
			},
		},
		{
			name:        "when trace for job-name is requested",
			args:        "lint -b main -p 123",
			expectedOut: "\nGetting job trace...\nShowing logs for lint job #1122.\nLorem ipsum",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{
							ID:     1122,
							Name:   "lint",
							Status: "failed",
						},
						{
							ID:     1124,
							Name:   "publish",
							Status: "failed",
						},
					}, lastPageResponse, nil)

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
			name:        "when trace for job-name and last pipeline is requested",
			args:        "lint -b main",
			expectedOut: "\nGetting job trace...\nShowing logs for lint job #1122.\nLorem ipsum",
			setupMock: func(tc *gitlabtesting.TestClient) {
				// GetPipelineWithFallback tries GetLatestPipeline first
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(&gitlab.Pipeline{
						ID: 123,
					}, nil, nil)
				// Check if pipeline has jobs
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1, Name: "test"},
					}, nil, nil)

				// GetJobId lists all jobs for the pipeline
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{
							ID:     1122,
							Name:   "lint",
							Status: "failed",
						},
						{
							ID:     1124,
							Name:   "publish",
							Status: "failed",
						},
					}, lastPageResponse, nil)

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdTrace,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			output, err := exec(tc.args)

			// THEN
			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			}

			assert.Equal(t, tc.expectedOut, output.String())
			assert.Empty(t, output.Stderr())
		})
	}
}
