//go:build !integration

package download

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils/upload"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const (
	assetName         = "asset.tar.gz"
	localContent      = "local_content"
	downloadedContent = "downloaded_content"
)

// serveAsset answers every request with status and, on success,
// downloadedContent. The server is on a different host from the GitLab
// client, so downloads go through the unauthenticated HTTP path.
func serveAsset(t *testing.T, status int) (string, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(downloadedContent))
		}
	}))
	t.Cleanup(testServer.Close)

	return testServer.URL + "/" + assetName, &hits
}

func downloadOneAsset(t *testing.T, dir, assetURL string, onExisting existingFileAction) (string, error) {
	t.Helper()

	gitlabClient, err := gitlab.NewClient("test-token")
	require.NoError(t, err)

	name := assetName
	io, _, stdout, _ := cmdtest.TestIOStreams()

	assets := []*upload.ReleaseAsset{{Name: &name, URL: &assetURL}}
	err = downloadAssets(t.Context(), gitlabClient, io, assets, dir, onExisting)

	return stdout.String(), err
}

func writeLocalFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(localContent), 0o644))
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, want, string(content))
}

// assertDirEntries also catches a temporary file that was not removed.
func assertDirEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	assert.ElementsMatch(t, want, got)
}

func TestDownload_ExistingFile(t *testing.T) {
	t.Parallel()

	t.Run("without a flag it fails and keeps the file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, assetName)
		writeLocalFile(t, path)
		assetURL, hits := serveAsset(t, http.StatusOK)

		_, err := downloadOneAsset(t, dir, assetURL, failOnExisting)

		require.ErrorIs(t, err, fs.ErrExist)
		require.EqualError(t, err, fmt.Sprintf("file %q already exists. Use '--clobber' to overwrite it, or '--skip-existing' to keep it: file already exists", path))
		assertFileContent(t, path, localContent)
		assertDirEntries(t, dir, assetName)
		assert.Zero(t, hits.Load(), "the asset must not be requested")
	})

	t.Run("--clobber overwrites the file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, assetName)
		require.NoError(t, os.WriteFile(path, []byte("stale_content_that_is_longer"), 0o644))
		assetURL, _ := serveAsset(t, http.StatusOK)

		_, err := downloadOneAsset(t, dir, assetURL, overwriteExisting)
		require.NoError(t, err)

		// A shorter download must not leave a tail of the previous file.
		assertFileContent(t, path, downloadedContent)
		assertDirEntries(t, dir, assetName)
	})

	t.Run("--clobber keeps the file when the server fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, assetName)
		writeLocalFile(t, path)
		assetURL, _ := serveAsset(t, http.StatusInternalServerError)

		_, err := downloadOneAsset(t, dir, assetURL, overwriteExisting)

		require.Error(t, err)
		assertFileContent(t, path, localContent)
		assertDirEntries(t, dir, assetName)
	})

	t.Run("--clobber replaces a symlink, not its target", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "target")
		writeLocalFile(t, target)
		path := filepath.Join(dir, assetName)
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("cannot create a symlink: %v", err)
		}
		assetURL, _ := serveAsset(t, http.StatusOK)

		_, err := downloadOneAsset(t, dir, assetURL, overwriteExisting)
		require.NoError(t, err)

		assertFileContent(t, target, localContent)
		info, err := os.Lstat(path)
		require.NoError(t, err)
		assert.True(t, info.Mode().IsRegular(), "the symlink must be replaced by a regular file")
		assertFileContent(t, path, downloadedContent)
	})

	t.Run("--skip-existing keeps the file and reports it", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, assetName)
		writeLocalFile(t, path)
		assetURL, hits := serveAsset(t, http.StatusOK)

		output, err := downloadOneAsset(t, dir, assetURL, keepExisting)
		require.NoError(t, err)

		assertFileContent(t, path, localContent)
		assert.Contains(t, output, "skipping file")
		assert.Zero(t, hits.Load(), "the asset must not be requested")
	})

	t.Run("--skip-existing downloads a missing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assetURL, _ := serveAsset(t, http.StatusOK)

		_, err := downloadOneAsset(t, dir, assetURL, keepExisting)
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(dir, assetName), downloadedContent)
	})

	t.Run("--clobber downloads a missing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assetURL, _ := serveAsset(t, http.StatusOK)

		_, err := downloadOneAsset(t, dir, assetURL, overwriteExisting)
		require.NoError(t, err)

		assertFileContent(t, filepath.Join(dir, assetName), downloadedContent)
	})
}

// The temporary file name must not grow with the asset name, or an asset
// name near the 255-byte file name limit fails with ENAMETOOLONG.
func TestDownload_LongAssetName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := strings.Repeat("a", 250)
	assetURL, _ := serveAsset(t, http.StatusOK)

	gitlabClient, err := gitlab.NewClient("test-token")
	require.NoError(t, err)

	err = downloadAsset(t.Context(), gitlabClient, assetURL, filepath.Join(dir, name), failOnExisting)
	require.NoError(t, err)

	assertFileContent(t, filepath.Join(dir, name), downloadedContent)
	assertDirEntries(t, dir, name)
}

func TestDownload_FailedDownloadLeavesNoFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		onExisting existingFileAction
	}{
		{name: "no flag", onExisting: failOnExisting},
		{name: "--clobber", onExisting: overwriteExisting},
		{name: "--skip-existing", onExisting: keepExisting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			assetURL, _ := serveAsset(t, http.StatusInternalServerError)

			_, err := downloadOneAsset(t, dir, assetURL, tt.onExisting)

			require.Error(t, err)
			assertDirEntries(t, dir)
		})
	}
}

// --skip-existing must not keep a file that a failed run left behind, or a
// re-run can never complete the download.
func TestDownload_SkipExistingAfterFailedDownload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	failingURL, _ := serveAsset(t, http.StatusInternalServerError)
	_, err := downloadOneAsset(t, dir, failingURL, keepExisting)
	require.Error(t, err)

	assetURL, hits := serveAsset(t, http.StatusOK)
	_, err = downloadOneAsset(t, dir, assetURL, keepExisting)
	require.NoError(t, err)

	assertFileContent(t, filepath.Join(dir, assetName), downloadedContent)
	assert.Equal(t, int32(1), hits.Load())
}

func TestDownloadCommand_ExistingFileFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flags       string
		wantErr     bool
		wantContent string
		wantHits    int32
	}{
		{name: "no flag", flags: "", wantErr: true, wantContent: localContent, wantHits: 0},
		{name: "--clobber", flags: "--clobber", wantContent: downloadedContent, wantHits: 1},
		{name: "--skip-existing", flags: "--skip-existing", wantContent: localContent, wantHits: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, assetName)
			writeLocalFile(t, path)
			assetURL, hits := serveAsset(t, http.StatusOK)

			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
			testClient.MockReleases.EXPECT().
				GetRelease("OWNER/REPO", "v1.0.0", gomock.Any()).
				Return(&gitlab.Release{
					TagName: "v1.0.0",
					Name:    "Release v1.0.0",
					Assets: gitlab.ReleaseAssets{
						Links: []*gitlab.ReleaseLink{{Name: assetName, URL: assetURL}},
					},
				}, nil, nil)

			exec := cmdtest.SetupCmdForTest(t, NewCmdDownload, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			_, err := exec(fmt.Sprintf("v1.0.0 --dir '%s' %s", dir, tt.flags))

			if tt.wantErr {
				require.ErrorContains(t, err, fmt.Sprintf("file %q already exists.", path))
			} else {
				require.NoError(t, err)
			}
			assertFileContent(t, path, tt.wantContent)
			assert.Equal(t, tt.wantHits, hits.Load())
		})
	}
}

func TestDownload_ClobberAndSkipExistingAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmdDownload, false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err := exec("v1.0.0 --clobber --skip-existing")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "clobber")
	assert.Contains(t, err.Error(), "skip-existing")
}
