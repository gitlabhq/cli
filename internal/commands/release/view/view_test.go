//go:build !integration

package view

import (
	"regexp"
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

func Test_ReleaseView(t *testing.T) {
	type testCase struct {
		name       string
		cli        string
		wantErr    bool
		wantStderr string
		setupMock  func(tc *gitlabtesting.TestClient)
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
		Author: gitlab.BasicUser{
			ID:        11809982,
			Username:  "test_user",
			Name:      "Test User",
			State:     "active",
			AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/11809982/avatar.png",
			WebURL:    "https://gitlab.com/test_user",
		},
		Commit: gitlab.Commit{
			ID:             "26e80b26fd9f8515401a4d10c331904f034e9f05",
			ShortID:        "26e80b26",
			Title:          "Add new feature",
			Message:        "Added a great new feature",
			AuthorName:     "Test User",
			AuthorEmail:    "test_user@gitlab.com",
			CommitterName:  "test_user",
			CommitterEmail: "test_user@gitlab.com",
			WebURL:         "https://gitlab.com/OWNER/REPO/-/commit/26e80b26fd9f8515401a4d10c331904f034e9f05",
		},
		CommitPath: "/OWNER/REPO/-/commit/26e80b26fd9f8515401a4d10c331904f034e9f05",
		TagPath:    "/OWNER/REPO/-/tags/0.0.1",
		Assets: gitlab.ReleaseAssets{
			Count: 2,
			Sources: []gitlab.ReleaseAssetsSource{
				{
					Format: "zip",
					URL:    "https://gitlab.com/OWNER/REPO/-/archive/0.0.1/REPO-0.0.1.zip",
				},
			},
			Links: []*gitlab.ReleaseLink{
				{
					ID:             1294469,
					Name:           "test asset",
					URL:            "https://gitlab.com/some/location/1133",
					DirectAssetURL: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1/downloads/test_asset",
					LinkType:       "other",
				},
			},
		},
		Links: gitlab.ReleaseLinks{
			Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1",
		},
	}

	testCases := []testCase{
		{
			name: "view release with specific tag",
			cli:  "0.0.1",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockReleases.EXPECT().
					GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).
					Return(testRelease, nil, nil)
			},
		},
		{
			name: "view latest release",
			cli:  "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockReleases.EXPECT().
					ListReleases("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.Release{testRelease}, nil, nil)
			},
		},
	}

	expectedOut := heredoc.Doc(`test_release
		Test User released this about X years ago
		26e80b26 - 0.0.1



		ASSETS
		test asset	https://gitlab.com/OWNER/REPO/-/releases/0.0.1/downloads/test_asset

		SOURCES
		https://gitlab.com/OWNER/REPO/-/archive/0.0.1/REPO-0.0.1.zip


		View this release on GitLab at https://gitlab.com/OWNER/REPO/-/releases/0.0.1
		`)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdView,
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

			// Normalize the time in the output
			outStr := out.String()
			timeRE := regexp.MustCompile(`\d+ years`)
			outStr = timeRE.ReplaceAllString(outStr, "X years")

			assert.Equal(t, expectedOut, outStr)
			assert.Empty(t, out.Stderr())
		})
	}
}

func TestReleaseView_JSON(t *testing.T) {
	t.Parallel()

	createdAt, _ := time.Parse(time.RFC3339, "2020-01-23T07:13:17.721Z")
	releasedAt, _ := time.Parse(time.RFC3339, "2020-01-23T07:13:17.721Z")

	testRelease := &gitlab.Release{
		Name:            "test_release",
		TagName:         "0.0.1",
		Description:     "Test description",
		CreatedAt:       &createdAt,
		ReleasedAt:      &releasedAt,
		UpcomingRelease: false,
		Author: gitlab.BasicUser{
			ID:       11809982,
			Username: "test_user",
			Name:     "Test User",
		},
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockReleases.EXPECT().
		GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).
		Return(testRelease, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdView,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("0.0.1 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"tag_name":"0.0.1"`)
	assert.Contains(t, out.String(), `"name":"test_release"`)
	assert.Contains(t, out.String(), `"description":"Test description"`)
	assert.Empty(t, out.Stderr())
}
