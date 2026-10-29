//go:build !integration

package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

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
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, doesFileExist(destPath), "partial asset file should be removed on failure")
}

func Test_downloadAsset_RemovesPartialFileOnErrorStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sameHost  bool
		assetPath string
	}{
		{
			name:      "other host",
			assetPath: "/asset.bin",
		},
		{
			name:      "markdown upload",
			sameHost:  true,
			assetPath: "/-/project/42/uploads/8a4f0b1c/file.tar.gz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer testServer.Close()

			baseURL := "https://gitlab.example.com"
			if tc.sameHost {
				baseURL = testServer.URL
			}
			gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(baseURL), gitlab.WithoutRetries())
			require.NoError(t, err)

			destPath := filepath.Join(t.TempDir(), "asset.bin")
			err = downloadAsset(t.Context(), gitlabClient, testServer.URL+tc.assetPath, destPath)

			require.Error(t, err)
			assert.False(t, doesFileExist(destPath), "partial asset file should be removed on an error status")
		})
	}
}

func Test_parseMarkdownUploadRef(t *testing.T) {
	t.Parallel()

	mustParse := func(t *testing.T, raw string) *url.URL {
		t.Helper()
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return u
	}

	tests := []struct {
		name     string
		baseURL  string
		assetURL string
		want     markdownUploadRef
		wantOK   bool
	}{
		{
			name:     "project ID form, GitLab 17.0 and later",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/-/project/42/uploads/8a4f0b1c/file.tar.gz",
			want:     markdownUploadRef{project: "42", secret: "8a4f0b1c", filename: "file.tar.gz"},
			wantOK:   true,
		},
		{
			name:     "namespace and project form, before GitLab 17.0",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/project/uploads/8a4f0b1c/file.tar.gz",
			want:     markdownUploadRef{project: "group/project", secret: "8a4f0b1c", filename: "file.tar.gz"},
			wantOK:   true,
		},
		{
			name:     "project in a subgroup",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/sub/project/uploads/8a4f0b1c/file.tar.gz",
			want:     markdownUploadRef{project: "group/sub/project", secret: "8a4f0b1c", filename: "file.tar.gz"},
			wantOK:   true,
		},
		{
			name:     "project in a nested subgroup",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/sub/subsub/project/uploads/8a4f0b1c/file.tar.gz",
			want:     markdownUploadRef{project: "group/sub/subsub/project", secret: "8a4f0b1c", filename: "file.tar.gz"},
			wantOK:   true,
		},
		{
			name:     "namespace and project form on an instance served from a subfolder",
			baseURL:  "https://example.com/gitlab/api/v4/",
			assetURL: "https://example.com/gitlab/group/project/uploads/8a4f0b1c/file.tar.gz",
			want:     markdownUploadRef{project: "group/project", secret: "8a4f0b1c", filename: "file.tar.gz"},
			wantOK:   true,
		},
		{
			name:     "instance served from a subfolder",
			baseURL:  "https://example.com/gitlab/api/v4/",
			assetURL: "https://example.com/gitlab/-/project/42/uploads/8a4f0b1c/file.tar.gz",
			want:     markdownUploadRef{project: "42", secret: "8a4f0b1c", filename: "file.tar.gz"},
			wantOK:   true,
		},
		{
			name:     "escaped characters in the file name",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/-/project/42/uploads/8a4f0b1c/my%20file.tar.gz",
			want:     markdownUploadRef{project: "42", secret: "8a4f0b1c", filename: "my file.tar.gz"},
			wantOK:   true,
		},
		{
			// A package registry asset is already served by the API, so it must
			// keep its existing download path.
			name:     "package registry link",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/api/v4/projects/42/packages/generic/pkg/1.0/file.tar.gz",
			wantOK:   false,
		},
		{
			name:     "release evidence link",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/project/-/releases/v1/evidences/1.json",
			wantOK:   false,
		},
		{
			name:     "raw repository file under an uploads directory",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/project/-/raw/main/uploads/images/logo.png",
			wantOK:   false,
		},
		{
			name:     "blob repository file under an uploads directory",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/project/-/blob/main/uploads/images/logo.png",
			wantOK:   false,
		},
		{
			name:     "raw repository file in a subgroup project",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/sub/project/-/raw/v1.0/docs/uploads/8a4f0b1c/file.tar.gz",
			wantOK:   false,
		},
		{
			name:     "job artifact under an uploads directory",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/project/-/jobs/7/artifacts/raw/uploads/8a4f0b1c/file.tar.gz",
			wantOK:   false,
		},
		{
			name:     "a path too short to name a project",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/uploads/8a4f0b1c/file.tar.gz",
			wantOK:   false,
		},
		{
			name:     "no file name after the secret",
			baseURL:  "https://gitlab.example.com/api/v4/",
			assetURL: "https://gitlab.example.com/group/project/uploads/8a4f0b1c/",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseMarkdownUploadRef(mustParse(t, tt.baseURL), mustParse(t, tt.assetURL))

			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// A Markdown upload link is a web route that only a browser session
// authenticates, so reading it with a token fails. The asset must come from
// the uploads API instead. See issue #1301.
func Test_downloadAsset_MarkdownUpload_UsesUploadsAPI(t *testing.T) {
	t.Parallel()

	const (
		secret   = "8a4f0b1c"
		apiPath  = "/api/v4/projects/42/uploads/" + secret + "/file.tar.gz"
		webPath  = "/-/project/42/uploads/" + secret + "/file.tar.gz"
		expected = "asset_payload"
	)

	var mu sync.Mutex
	var requested []string

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requested = append(requested, r.URL.Path)
		mu.Unlock()

		if r.URL.Path == apiPath {
			if r.Header.Get("Private-Token") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(expected))
			return
		}

		// The web route redirects a token request to the sign-in page, which
		// answers 404 for a private project.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer testServer.Close()

	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL))
	require.NoError(t, err)

	destPath := filepath.Join(t.TempDir(), "file.tar.gz")
	err = downloadAsset(t.Context(), gitlabClient, testServer.URL+webPath, destPath)
	require.NoError(t, err)

	content, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, expected, string(content))

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, requested, apiPath, "the asset should come from the uploads API")
	assert.NotContains(t, requested, webPath, "the web route should not be requested")
}

// Links that are not Markdown uploads keep their existing download path.
func Test_downloadAsset_SameHost_NonUploadLinkIsUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		assetPath string
	}{
		{
			name:      "package registry link",
			assetPath: "/api/v4/projects/42/packages/generic/pkg/1.0/file.tar.gz",
		},
		{
			name:      "raw repository file under an uploads directory",
			assetPath: "/group/project/-/raw/main/uploads/images/logo.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			var requested []string

			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requested = append(requested, r.URL.Path)
				mu.Unlock()
				_, _ = w.Write([]byte("asset_payload"))
			}))
			defer testServer.Close()

			gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL))
			require.NoError(t, err)

			destPath := filepath.Join(t.TempDir(), "asset")
			err = downloadAsset(t.Context(), gitlabClient, testServer.URL+tt.assetPath, destPath)
			require.NoError(t, err)

			content, err := os.ReadFile(destPath)
			require.NoError(t, err)
			assert.Equal(t, "asset_payload", string(content))

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, []string{tt.assetPath}, requested)
		})
	}
}
