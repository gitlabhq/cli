//go:build !integration

package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ScheduleEdit(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:        "Schedule updated",
			cli:         "1 --cron '*0 * * * *' --description 'example pipeline' --ref 'main'",
			expectedMsg: []string{"Updated schedule with ID 1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelineSchedules.EXPECT().
					EditPipelineSchedule("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.PipelineSchedule{ID: 1}, nil, nil)
			},
		},
		{
			name:        "Schedule updated with new variable",
			cli:         "1 --description 'example pipeline' --create-variable 'foo:bar'",
			expectedMsg: []string{"Updated schedule with ID 1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelineSchedules.EXPECT().
					EditPipelineSchedule("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.PipelineSchedule{ID: 1}, nil, nil)
				tc.MockPipelineSchedules.EXPECT().
					CreatePipelineScheduleVariable("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.PipelineVariable{}, nil, nil)
			},
		},
		{
			name:        "Schedule updated with updated variable",
			cli:         "1 --description 'example pipeline' --update-variable 'foo:bar'",
			expectedMsg: []string{"Updated schedule with ID 1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelineSchedules.EXPECT().
					EditPipelineSchedule("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.PipelineSchedule{ID: 1}, nil, nil)
				tc.MockPipelineSchedules.EXPECT().
					EditPipelineScheduleVariable("OWNER/REPO", int64(1), "foo", gomock.Any()).
					Return(&gitlab.PipelineVariable{}, nil, nil)
			},
		},
		{
			name:        "Schedule updated with deleted variable",
			cli:         "1 --description 'example pipeline' --delete-variable 'foo'",
			expectedMsg: []string{"Updated schedule with ID 1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelineSchedules.EXPECT().
					EditPipelineSchedule("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.PipelineSchedule{ID: 1}, nil, nil)
				tc.MockPipelineSchedules.EXPECT().
					DeletePipelineScheduleVariable("OWNER/REPO", int64(1), "foo").
					Return(nil, nil, nil)
			},
		},
		{
			name:       "Schedule updated with invalid variable format - create",
			cli:        "1 --create-variable 'foo:bar' --create-variable 'foo'",
			wantErr:    true,
			wantStderr: "invalid format for --create-variable: foo",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:       "Schedule updated with invalid variable format - update",
			cli:        "1 --update-variable 'foo:bar' --update-variable 'foo'",
			wantErr:    true,
			wantStderr: "invalid format for --update-variable: foo",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:        "Schedule not changed if no flags are set",
			cli:         "1",
			expectedMsg: []string{"Updated schedule with ID 1"},
			setupMock:   func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdUpdate,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantStderr, err.Error())
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}
