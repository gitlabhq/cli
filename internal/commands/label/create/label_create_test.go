//go:build !integration

package create

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_LabelCreate(t *testing.T) {
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
			name:        "Label created",
			cli:         "--name foo --color red",
			expectedMsg: []string{"Created label: foo\nWith color: #FF0000"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().
					CreateLabel("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Label{Name: "foo", Color: "#FF0000"}, nil, nil)
			},
		},
		{
			name:        "Label not created because of missing name",
			cli:         "",
			wantErr:     true,
			wantStderr:  "required flag(s) \"name\" not set",
			expectedMsg: []string{""},
			setupMock:   func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:        "Label created with description",
			cli:         "--name foo --color red --description foo_desc",
			expectedMsg: []string{"Created label: foo\nWith color: #FF0000"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().
					CreateLabel("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Label{Name: "foo", Color: "#FF0000", Description: "foo_desc"}, nil, nil)
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
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}
