//go:build !integration

package delete

import (
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ReleaseDelete(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	createdAt, _ := time.Parse(time.RFC3339, "2020-01-23T07:13:17.721Z")
	releasedAt, _ := time.Parse(time.RFC3339, "2020-01-23T07:13:17.721Z")

	testRelease := &gitlab.Release{
		Name:            "test_release",
		TagName:         "0.0.1",
		Description:     "",
		CreatedAt:       &createdAt,
		ReleasedAt:      &releasedAt,
		UpcomingRelease: false,
	}

	testCases := []testCase{
		{
			name: "delete a release",
			cli:  "0.0.1 --yes",
			expectedOut: heredoc.Doc(`• Validating tag repo=OWNER/REPO tag=0.0.1
				• Deleting release repo=OWNER/REPO tag=0.0.1
				✓ Release "test_release" deleted.
			`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockReleases.EXPECT().
					GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).
					Return(testRelease, nil, nil)
				tc.MockReleases.EXPECT().
					DeleteRelease("OWNER/REPO", "0.0.1").
					Return(testRelease, nil, nil)
			},
		},
		{
			name: "delete a release and associated tag",
			cli:  "0.0.1 --yes --with-tag",
			expectedOut: heredoc.Doc(`• Validating tag repo=OWNER/REPO tag=0.0.1
				• Deleting release repo=OWNER/REPO tag=0.0.1
				✓ Release "test_release" deleted.
				• Deleting associated tag "0.0.1".
				✓ Tag "test_release" deleted.
			`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockReleases.EXPECT().
					GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).
					Return(testRelease, nil, nil)
				tc.MockReleases.EXPECT().
					DeleteRelease("OWNER/REPO", "0.0.1").
					Return(testRelease, nil, nil)
				tc.MockTags.EXPECT().
					DeleteTag("OWNER/REPO", "0.0.1").
					Return(nil, nil)
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
				NewCmdDelete,
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
			assert.Equal(t, tc.expectedOut, out.String())
			assert.Empty(t, out.Stderr())
		})
	}
}
