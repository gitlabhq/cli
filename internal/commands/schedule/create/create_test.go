//go:build !integration

package create

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ScheduleCreate(t *testing.T) {
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
			name:        "Schedule created",
			cli:         "--cron '*0 * * * *' --description 'example pipeline' --ref 'main'",
			expectedMsg: []string{"Created schedule with ID 2"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelineSchedules.EXPECT().
					CreatePipelineSchedule("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(&gitlab.PipelineSchedule{ID: 2}, nil, nil)
			},
		},
		{
			name:        "Schedule not created because of missing ref",
			cli:         "--cron '*0 * * * *' --description 'example pipeline'",
			wantErr:     true,
			wantStderr:  "required flag(s) \"ref\" not set",
			expectedMsg: []string{""},
			setupMock:   func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:       "Schedule not created because of invalid variable",
			cli:        "--cron '*0 * * * *' --description 'example pipeline' --ref 'main' --variable 'foo'",
			wantErr:    true,
			wantStderr: "invalid format for --variable: foo",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:       "Schedule not created when a later variable is invalid",
			cli:        "--cron '*0 * * * *' --description 'example pipeline' --ref 'main' --variable 'foo:bar' --variable 'invalid'",
			wantErr:    true,
			wantStderr: "invalid format for --variable: invalid",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:        "Schedule created with variable",
			cli:         "--cron '*0 * * * *' --description 'example pipeline' --ref 'main' --variable 'foo:bar'",
			expectedMsg: []string{"Created schedule"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				gomock.InOrder(
					tc.MockPipelineSchedules.EXPECT().
						CreatePipelineSchedule("OWNER/REPO", gomock.Any(), gomock.Any()).
						Return(&gitlab.PipelineSchedule{ID: 0}, nil, nil),
					tc.MockPipelineSchedules.EXPECT().
						CreatePipelineScheduleVariable("OWNER/REPO", int64(0), gomock.Any(), gomock.Any()).
						Return(&gitlab.PipelineVariable{}, nil, nil),
				)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdCreate,
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
