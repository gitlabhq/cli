//go:build !integration

package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils/upload"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func doesFileExist(fileName string) bool {
	_, err := os.Stat(fileName)
	return err == nil
}

func TestDownloadCommand_WithTag_NoAssets(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockReleases.EXPECT().
		GetRelease("OWNER/REPO", "v1.0.0", gomock.Any()).
		Return(&gitlab.Release{
			TagName: "v1.0.0",
			Name:    "Release v1.0.0",
			Assets: gitlab.ReleaseAssets{
				Links:   []*gitlab.ReleaseLink{},
				Sources: []gitlab.ReleaseAssetsSource{},
			},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDownload, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("v1.0.0")
	require.NoError(t, err)

	// No assets to download, so it should succeed with a warning
	assert.Contains(t, output.String(), "no release assets found")
}

func TestDownloadCommand_LatestRelease(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockReleases.EXPECT().
		ListReleases("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Release{
			{
				TagName: "v2.0.0",
				Name:    "Release v2.0.0",
				Assets: gitlab.ReleaseAssets{
					Links:   []*gitlab.ReleaseLink{},
					Sources: []gitlab.ReleaseAssetsSource{},
				},
			},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDownload, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("")
	require.NoError(t, err)

	// No assets to download, so it should succeed with a warning
	assert.Contains(t, output.String(), "no release assets found")
}

func TestDownloadCommand_NoReleases(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockReleases.EXPECT().
		ListReleases("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Release{}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDownload, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err := exec("")
	require.Error(t, err)
	// The WrapError returns the underlying error message ("not found")
	assert.Contains(t, err.Error(), "not found")
}

func TestDownloadCommand_ReleaseNotFound(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	notFoundResp := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusNotFound},
	}
	testClient.MockReleases.EXPECT().
		GetRelease("OWNER/REPO", "v999.0.0", gomock.Any()).
		Return(nil, notFoundResp, gitlab.ErrNotFound)

	exec := cmdtest.SetupCmdForTest(t, NewCmdDownload, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err := exec("v999.0.0")
	require.Error(t, err)
	// The WrapError returns the underlying error message
	assert.Error(t, err)
}

func TestDownloadAsset_InvalidURL(t *testing.T) {
	t.Parallel()

	client, err := gitlab.NewClient("test-token")
	require.NoError(t, err)

	destinationPath := filepath.Join(t.TempDir(), "asset")
	err = downloadAsset(t.Context(), client, "%", destinationPath)

	require.EqualError(t, err, `parsing release asset URL: parse "%": invalid URL escape "%"`)
	assert.NoFileExists(t, destinationPath)
}

// Test_downloadAssets tests the internal downloadAssets function for path sanitization
// and file download behavior. Uses httptest.NewServer for HTTP mocking since
// this tests raw HTTP download functionality, not GitLab API calls.
func Test_downloadAssets(t *testing.T) {
	// Cannot use t.Parallel() because subtests share the test server

	// Create a test HTTP server that serves file content
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test_data"))
	}))
	defer testServer.Close()

	// Create a GitLab client with the test server's URL
	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL))
	require.NoError(t, err)

	tests := []struct {
		name     string
		filename string
		want     string
		wantErr  bool
	}{
		{
			name:     "A regular filename",
			filename: "cli-v1.22.0.tar",
			want:     "cli-v1.22.0.tar",
		},
		{
			name:     "A filename with directory traversal",
			filename: "cli-v1.../../22.0.tar",
			want:     "22.0.tar",
		},
		{
			name:     "A particularly nasty filename",
			filename: "..././..././..././etc/password_file",
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Do not use t.Parallel() here - test server must stay alive

			fullURL := testServer.URL + "/" + tt.filename

			io, _, _, _ := cmdtest.TestIOStreams()

			release := &upload.ReleaseAsset{
				Name: &tt.filename,
				URL:  &fullURL,
			}

			releases := []*upload.ReleaseAsset{release}

			tempPath := t.TempDir()

			filePathWanted := filepath.Join(tempPath, tt.want)

			err := downloadAssets(t.Context(), gitlabClient, io, releases, tempPath)

			if tt.wantErr {
				assert.Error(t, err, "Should error out if a path doesn't exist")
				return
			}

			require.NoError(t, err, "Should not have errors")
			assert.True(t, doesFileExist(filePathWanted), "File should exist")
		})
	}
}

func Test_downloadAsset_SameHost_UsesContextCancellation(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer testServer.Close()

	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	destPath := filepath.Join(t.TempDir(), "asset.bin")
	err = downloadAsset(ctx, gitlabClient, testServer.URL+"/asset.bin", destPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
