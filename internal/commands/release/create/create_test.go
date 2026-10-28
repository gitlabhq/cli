//go:build !integration

package create

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils/upload"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestReleaseCreate(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	tests := []struct {
		name string
		cli  string

		expectedDescription string
		expectedTagMessage  string
	}{
		{
			name: "when a release is created",
			cli:  "0.0.1 --notes \"test release\"",
		},
		{
			name:                "when a release is created with a description",
			cli:                 `0.0.1 --notes "bugfix release"`,
			expectedDescription: "bugfix release",
		},
		{
			name:               "when a release is created with a tag message",
			cli:                `0.0.1 --notes "test release" --tag-message "tag message"`,
			expectedTagMessage: "tag message",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdCreate,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

			// Tag exists
			testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

			// Release doesn't exist
			testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

			// Create release - verify options
			testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).
				DoAndReturn(func(pid any, opts *gitlab.CreateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
					assert.Equal(t, "0.0.1", *opts.TagName)
					if tc.expectedDescription != "" {
						require.NotNil(t, opts.Description)
						assert.Equal(t, tc.expectedDescription, *opts.Description)
					}
					if tc.expectedTagMessage != "" {
						require.NotNil(t, opts.TagMessage)
						assert.Equal(t, tc.expectedTagMessage, *opts.TagMessage)
					}
					return &gitlab.Release{
						Name:        "test_release",
						TagName:     "0.0.1",
						Description: "bugfix release",
						Links:       gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
					}, nil, nil
				})

			output, err := exec(tc.cli)

			if assert.NoErrorf(t, err, "error running command `create %s`: %v", tc.cli, err) {
				assert.Contains(t, output.String(), `• Validating tag 0.0.1
• Creating or updating release repo=OWNER/REPO tag=0.0.1
✓ Release created:	url=https://gitlab.com/OWNER/REPO/-/releases/0.0.1
✓ Release succeeded after`)
				assert.Empty(t, output.Stderr())
			}
		})
	}
}

func TestReleaseCreate_LongReleasedAtReturnsError(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
	testClient.MockTags.EXPECT().
		GetTag("OWNER/REPO", "0.0.1", gomock.Any()).
		Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)
	testClient.MockReleases.EXPECT().
		GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).
		Return(nil, notFoundResponse, errors.New("not found"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdCreate,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`0.0.1 --notes "test release" --released-at "not-a-valid-timestamp-that-is-long"`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse")
}

func TestReleaseCreate_ReturnsTagTransportError(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockTags.EXPECT().
		GetTag("OWNER/REPO", "0.0.1", gomock.Any()).
		Return(nil, nil, errors.New("network unavailable"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdCreate,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`0.0.1 --notes "test release"`)

	require.ErrorContains(t, err, "network unavailable")
}

func TestReleaseCreateWithFiles(t *testing.T) {
	// NOTE: This test cannot use t.Parallel() because it uses t.Setenv().
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	tests := []struct {
		name string
		cli  string

		wantType     bool
		expectedType gitlab.LinkTypeValue
	}{
		{
			name: "when a release is created and a file is uploaded using filename only",
			cli:  "0.0.1 --notes \"test release\" testdata/test_file.txt",

			wantType: false,
		},
		{
			name: "when a release is created and a filename is uploaded with display name and type",
			cli:  "0.0.1 --notes \"test release\" testdata/test_file.txt#test_file#other",

			wantType:     true,
			expectedType: gitlab.OtherLinkType,
		},
		{
			name: "when a release is created and a filename is uploaded with a type",
			cli:  "0.0.1 --notes \"test release\" testdata/test_file.txt##package",

			wantType:     true,
			expectedType: gitlab.PackageLinkType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))

			notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

			// Tag exists
			testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

			// Release doesn't exist
			testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

			// Mock UploadProjectMarkdown for file upload
			uploadCall := testClient.MockProjectMarkdownUploads.EXPECT().
				UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "test_file.txt", gomock.Any()).
				Return(&gitlab.ProjectMarkdownUploadedFile{
					Alt:      "test_file",
					URL:      "/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt",
					FullPath: "/namespace1/project1/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt",
					Markdown: "![test_file](/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt)",
				}, nil, nil)

			// The file link goes in CreateRelease. Any CreateReleaseLink call fails the test.
			testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).
				After(uploadCall.Call).
				DoAndReturn(func(pid any, opts *gitlab.CreateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
					assert.Equal(t, "0.0.1", *opts.TagName)
					require.NotNil(t, opts.Assets)
					require.Len(t, opts.Assets.Links, 1)
					link := opts.Assets.Links[0]
					assert.Equal(t, "https://gitlab.com/namespace1/project1/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt", *link.URL)
					assert.Equal(t, "/test_file.txt", *link.DirectAssetPath)
					if tc.wantType {
						require.NotNil(t, link.LinkType)
						assert.Equal(t, tc.expectedType, *link.LinkType)
					} else {
						assert.Nil(t, link.LinkType)
					}

					return &gitlab.Release{
						Name:    "test_release",
						TagName: "0.0.1",
						Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
						Assets: gitlab.ReleaseAssets{
							Links: []*gitlab.ReleaseLink{{
								ID:             2,
								Name:           "test_file.txt",
								URL:            "https://gitlab.com/namespace1/project1/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt",
								DirectAssetURL: "https://gitlab.example.com/mynamespace/hello/-/releases/0.0.1/downloads/test_file.txt",
								LinkType:       gitlab.OtherLinkType,
							}},
						},
					}, nil, nil
				})

			exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tc.cli)

			if assert.NoErrorf(t, err, "error running command `create %s`: %v", tc.cli, err) {
				assert.Contains(t, output.String(), `• Validating tag 0.0.1
• Creating or updating release repo=OWNER/REPO tag=0.0.1
• Uploading to release	file=testdata/test_file.txt name=test_file.txt
✓ Release created:	url=https://gitlab.com/OWNER/REPO/-/releases/0.0.1
✓ Added release asset	name=test_file.txt url=https://gitlab.example.com/mynamespace/hello/-/releases/0.0.1/downloads/test_file.txt
✓ Release succeeded after`)
				assert.Empty(t, output.Stderr())
			}
		})
	}
}

func TestReleaseCreate_WithAssetsLinksJSON(t *testing.T) {
	// NOTE: This test cannot use t.Parallel() because it uses t.Setenv().
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	tests := []struct {
		name           string
		cli            string
		expectedOutput string
		wantConflict   bool
	}{
		{
			name: "with direct_asset_path",
			cli:  `0.0.1 --notes "test release" --assets-links='[{"name": "any-name", "url": "https://example.com/any-asset-url", "direct_asset_path": "/any-path"}]'`,
			expectedOutput: `• Validating tag 0.0.1
• Creating or updating release repo=OWNER/REPO tag=0.0.1
✓ Release created:	url=https://gitlab.com/OWNER/REPO/-/releases/0.0.1
✓ Added release asset	name=any-name url=https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path
✓ Release succeeded after`,
		},
		{
			name: "with filepath aliased to direct_asset_path",
			cli:  `0.0.1 --notes "test release" --assets-links='[{"name": "any-name", "url": "https://example.com/any-asset-url", "filepath": "/any-path"}]'`,
			expectedOutput: `• Validating tag 0.0.1
• Creating or updating release repo=OWNER/REPO tag=0.0.1
✓ Release created:	url=https://gitlab.com/OWNER/REPO/-/releases/0.0.1
✓ Added release asset	name=any-name url=https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path
	! Aliased deprecated ` + "`filepath`" + ` field to ` + "`direct_asset_path`" + `. Replace ` + "`filepath`" + ` with ` + "`direct_asset_path`" + `	name=any-name
✓ Release succeeded after`,
		},
		{
			name:         "with both filepath and direct_asset_path",
			cli:          `0.0.1 --notes "test release" testdata/test_file.txt --assets-links='[{"name": "any-name", "url": "https://example.com/any-asset-url", "filepath": "/any-path", "direct_asset_path": "/any-path"}]'`,
			wantConflict: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))

			notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

			// Tag exists
			testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

			// Release doesn't exist
			testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

			exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			if tt.wantConflict {
				// No upload or CreateRelease expectation: either call fails the test.
				_, err := exec(tt.cli)

				var conflictErr *upload.ConflictDirectAssetPathError
				require.ErrorAs(t, err, &conflictErr)
				return
			}

			// The links go in CreateRelease. Any CreateReleaseLink call fails the test.
			testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).
				DoAndReturn(func(pid any, opts *gitlab.CreateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
					require.NotNil(t, opts.Assets)
					require.Len(t, opts.Assets.Links, 1)
					link := opts.Assets.Links[0]
					assert.Equal(t, "any-name", *link.Name)
					assert.Equal(t, "https://example.com/any-asset-url", *link.URL)
					require.NotNil(t, link.DirectAssetPath)
					assert.Equal(t, "/any-path", *link.DirectAssetPath)
					assert.Nil(t, link.FilePath)

					return &gitlab.Release{
						Name:    "test_release",
						TagName: "0.0.1",
						Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
						Assets: gitlab.ReleaseAssets{
							Links: []*gitlab.ReleaseLink{{
								ID:             1,
								Name:           "any-name",
								URL:            "https://example.com/any-asset-url",
								DirectAssetURL: "https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path",
								LinkType:       gitlab.OtherLinkType,
							}},
						},
					}, nil, nil
				})

			output, err := exec(tt.cli)

			if assert.NoErrorf(t, err, "error running command `create %s`: %v", tt.cli, err) {
				assert.Contains(t, output.String(), tt.expectedOutput)
				assert.Empty(t, output.Stderr())
			}
		})
	}
}

func TestReleaseCreate_FilesAndAssetsLinksJSON(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

	testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)
	testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

	uploadCall := testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "test_file.txt", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{FullPath: "/-/project/1/uploads/abc/test_file.txt"}, nil, nil)

	// Both links go in CreateRelease. Any CreateReleaseLink call fails the test.
	testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).
		After(uploadCall.Call).
		DoAndReturn(func(pid any, opts *gitlab.CreateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
			require.NotNil(t, opts.Assets)
			require.Len(t, opts.Assets.Links, 2)
			assert.Equal(t, "any-name", *opts.Assets.Links[0].Name)
			assert.Equal(t, "https://example.com/any-asset-url", *opts.Assets.Links[0].URL)
			assert.Equal(t, "test_file.txt", *opts.Assets.Links[1].Name)
			assert.Equal(t, "https://gitlab.com/-/project/1/uploads/abc/test_file.txt", *opts.Assets.Links[1].URL)

			return &gitlab.Release{
				Name:    "test_release",
				TagName: "0.0.1",
				Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	output, err := exec(`0.0.1 --notes "test release" testdata/test_file.txt --assets-links='[{"name": "any-name", "url": "https://example.com/any-asset-url"}]'`)

	require.NoError(t, err)
	assert.Contains(t, output.String(), "Release created:")
	assert.Empty(t, output.Stderr())
}

func TestReleaseCreate_UpdateWithAssetsLinksJSON(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	okResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}
	existingRelease := &gitlab.Release{
		Name:    "0.0.1",
		TagName: "0.0.1",
		Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
	}

	// Tag exists
	testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

	testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(existingRelease, okResponse, nil)

	// UpdateRelease has no assets option. Any CreateRelease call fails the test.
	testClient.MockReleases.EXPECT().UpdateRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(existingRelease, okResponse, nil)

	testClient.MockReleaseLinks.EXPECT().
		CreateReleaseLink("OWNER/REPO", "0.0.1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(pid any, tagName string, opts *gitlab.CreateReleaseLinkOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ReleaseLink, *gitlab.Response, error) {
			assert.Equal(t, "any-name", *opts.Name)
			assert.Equal(t, "https://example.com/any-asset-url", *opts.URL)
			require.NotNil(t, opts.DirectAssetPath)
			assert.Equal(t, "/any-path", *opts.DirectAssetPath)

			return &gitlab.ReleaseLink{
				ID:             1,
				Name:           "any-name",
				URL:            "https://example.com/any-asset-url",
				DirectAssetURL: "https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path",
			}, nil, nil
		}).
		Times(1)

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	output, err := exec(`0.0.1 --notes "test release" --assets-links='[{"name": "any-name", "url": "https://example.com/any-asset-url", "direct_asset_path": "/any-path"}]'`)
	require.NoError(t, err)
	assert.Contains(t, output.String(), "✓ Release updated")
	assert.Contains(t, output.String(), "✓ Added release asset\tname=any-name url=https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path")
	assert.Empty(t, output.Stderr())
}

func TestReleaseCreate_InvalidAssetsLinksJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		assetsLinks string
		wantErr     string
	}{
		{
			name:        "only a null link",
			assetsLinks: `[null]`,
			wantErr:     "failed to parse JSON string: the asset link at index 0 is null",
		},
		{
			name:        "a null link after a valid one",
			assetsLinks: `[{"name": "any-name", "url": "https://example.com/any-asset-url"}, null]`,
			wantErr:     "failed to parse JSON string: the asset link at index 1 is null",
		},
		{
			name:        "malformed JSON",
			assetsLinks: `[{"name": "any-name"`,
			wantErr:     "failed to parse JSON string: unexpected end of JSON input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// No expectations: the links are rejected before any request is sent.
			testClient := gitlabtesting.NewTestClient(t)

			exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			_, err := exec(`0.0.1 --notes "test release" --assets-links='` + tt.assetsLinks + `'`)

			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestReleaseCreateWithPublishToCatalog(t *testing.T) {
	// This test uses httptest.NewServer because the catalog publish API uses raw HTTP
	// via client.Do() rather than a GitLab client-go service interface.

	tests := []struct {
		name string
		cli  string

		wantOutput string
		wantBody   string
		wantErr    bool
		errMsg     string
	}{
		{
			name: "with version",
			cli:  "0.0.1 --notes \"test release\" --publish-to-catalog",
			wantBody: `{
				"version": "0.0.1",
				"metadata": {
					"components": [
						{
							"component_type": "template",
							"name": "component-1",
							"spec": {
								"inputs": {
									"compiler": {
										"default": "gcc"
									}
								}
							}
						},
						{
							"component_type": "template",
							"name": "component-2",
							"spec": null
						},
						{
							"component_type": "template",
							"name": "component-3",
							"spec": {
								"inputs": {
									"test_framework": {
										"default": "unittest"
									}
								}
							}
						}
					]
				}
			}`,
			wantOutput: `• Publishing release tag=0.0.1 to the GitLab CI/CD catalog for repo=OWNER/REPO...
✓ Release published: url=https://gitlab.example.com/explore/catalog/my-namespace/my-component-project`,
		},
	}

	originalWd, err := os.Getwd()
	require.NoError(t, err)

	t.Chdir(filepath.Join(originalWd, "..", "..", "project", "publish", "catalog", "testdata", "test-repo"))

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test server that handles both release APIs and catalog publish
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/OWNER/REPO/repository/tags/0.0.1":
					// Tag exists
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"name": "0.0.1"}`))

				case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/OWNER/REPO/releases/0.0.1":
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))

				case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/OWNER/REPO/releases":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{
						"name": "test_release",
						"tag_name": "0.0.1",
						"description": "bugfix release",
						"created_at": "2023-01-19T02:58:32.622Z",
						"released_at": "2023-01-19T02:58:32.622Z",
						"upcoming_release": false,
						"tag_path": "/OWNER/REPO/-/tags/0.0.1",
						"_links": {
							"self": "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"
						}
					}`))

				case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/OWNER/REPO/catalog/publish":
					if tc.wantBody != "" {
						var reqBody, expectedBody map[string]any
						err := json.NewDecoder(r.Body).Decode(&reqBody)
						assert.NoError(t, err)
						err = json.Unmarshal([]byte(tc.wantBody), &expectedBody)
						assert.NoError(t, err)

						reqBodyJSON, _ := json.Marshal(reqBody)
						expectedBodyJSON, _ := json.Marshal(expectedBody)
						assert.JSONEq(t, string(expectedBodyJSON), string(reqBodyJSON))
					}

					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{
						"catalog_url": "https://gitlab.example.com/explore/catalog/my-namespace/my-component-project"
					}`))

				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer testServer.Close()

			// Create a GitLab client with the test server's URL
			gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
			require.NoError(t, err)

			exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
				cmdtest.WithGitLabClient(gitlabClient),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tc.cli)

			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.errMsg, err.Error())
			} else {
				require.NoError(t, err)
				assert.Contains(t, output.String(), tc.wantOutput)
			}
		})
	}
}

func TestReleaseCreate_NoUpdate(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	t.Run("when release doesn't exist with --no-update flag", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

		// Tag exists, so no need to get default branch
		tc.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)
		tc.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
		tc.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).Return(&gitlab.Release{
			Name:    "test_release",
			TagName: "0.0.1",
			Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
		}, nil, nil)

		output, err := exec("0.0.1 --notes \"test release\" --no-update")
		require.NoError(t, err)
		assert.Contains(t, output.String(), "Release created:")
	})

	t.Run("when release exists with --no-update flag", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		okResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}

		// Tag exists
		tc.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)
		// Release exists
		tc.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Release{
			Name:    "test_release",
			TagName: "0.0.1",
			Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
		}, okResponse, nil)

		_, err := exec("0.0.1 --notes \"test release\" --no-update")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "release for tag \"0.0.1\" already exists and --no-update flag was specified")
	})
}

func TestReleaseCreate_MilestoneClosing(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	t.Run("successfully closes milestone after release", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

		// Tag exists
		testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

		// Release doesn't exist
		testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

		testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).Return(&gitlab.Release{
			Name:        "0.0.1",
			TagName:     "0.0.1",
			Description: "Release with milestone",
			Links:       gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
		}, nil, nil)

		// List milestones to find the one to close
		testClient.MockMilestones.EXPECT().ListMilestones("OWNER/REPO", gomock.Any()).Return([]*gitlab.Milestone{
			{
				ID:    1,
				IID:   1,
				Title: "v1.0",
				State: "active",
			},
		}, nil, nil)

		// Update milestone to closed state
		testClient.MockMilestones.EXPECT().UpdateMilestone("OWNER/REPO", int64(1), gomock.Any()).Return(&gitlab.Milestone{
			ID:    1,
			IID:   1,
			Title: "v1.0",
			State: "closed",
		}, nil, nil)

		output, err := exec("0.0.1 --notes \"test release\" --milestone 'v1.0'")

		require.NoError(t, err)
		assert.Contains(t, output.String(), `• Validating tag 0.0.1
• Creating or updating release repo=OWNER/REPO tag=0.0.1
✓ Release created:	url=https://gitlab.com/OWNER/REPO/-/releases/0.0.1
✓ Closed milestone "v1.0"`)
	})

	t.Run("skips milestone closing when --no-close-milestone is set", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

		// Tag exists
		testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

		// Release doesn't exist
		testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

		testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).Return(&gitlab.Release{
			Name:        "0.0.1",
			TagName:     "0.0.1",
			Description: "Release with milestone",
			Links:       gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
		}, nil, nil)

		// No milestone API calls expected when --no-close-milestone is set

		output, err := exec("0.0.1 --notes \"test release\" --milestone 'v1.0' --no-close-milestone")

		require.NoError(t, err)
		assert.Contains(t, output.String(), `• Validating tag 0.0.1
• Creating or updating release repo=OWNER/REPO tag=0.0.1
✓ Release created:	url=https://gitlab.com/OWNER/REPO/-/releases/0.0.1
✓ Skipping closing milestones`)
	})

	for _, tt := range []struct {
		name       string
		status     int
		authSource gitlab.AuthSource
		hint       bool
	}{
		{name: "job token unauthorized", status: http.StatusUnauthorized, authSource: gitlab.JobTokenAuthSource{Token: "job-token"}, hint: true},
		{name: "job token forbidden", status: http.StatusForbidden, authSource: gitlab.JobTokenAuthSource{Token: "job-token"}, hint: true},
		{name: "job token server error", status: http.StatusInternalServerError, authSource: gitlab.JobTokenAuthSource{Token: "job-token"}},
		{name: "access token unauthorized", status: http.StatusUnauthorized, authSource: gitlab.AccessTokenAuthSource{Token: "pat"}},
		{name: "access token forbidden", status: http.StatusForbidden, authSource: gitlab.AccessTokenAuthSource{Token: "pat"}},
	} {
		t.Run("explains failed milestone lookup/"+tt.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdCreate,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, tt.authSource, glinstance.DefaultHostname)),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
			testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)
			testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
			testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).Return(&gitlab.Release{
				TagName: "0.0.1",
				Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
			}, nil, nil)
			testClient.MockMilestones.EXPECT().ListMilestones("OWNER/REPO", gomock.Any()).
				Return(nil, nil, &gitlab.ErrorResponse{StatusCode: tt.status, Message: http.StatusText(tt.status)})

			output, err := exec("0.0.1 --notes \"test release\" --milestone 'v1.0'")

			require.NoError(t, err)
			if tt.hint {
				assert.Contains(t, output.Stderr(), "A CI_JOB_TOKEN cannot access the Milestones API")
				assert.Contains(t, output.Stderr(), "--no-close-milestone")
			} else {
				assert.NotContains(t, output.Stderr(), "CI_JOB_TOKEN")
			}
		})
	}
}

func TestReleaseCreate_WithoutNotes(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	t.Run("create release without notes in non-interactive mode", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

		// Tag exists
		testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

		// Release doesn't exist
		testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

		// Create release without description
		testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).
			DoAndReturn(func(pid any, opts *gitlab.CreateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
				// Description should not be set (or be empty)
				if opts.Description != nil {
					assert.Empty(t, *opts.Description)
				}
				return &gitlab.Release{
					Name:    "0.0.1",
					TagName: "0.0.1",
					Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
				}, nil, nil
			})

		output, err := exec("0.0.1")
		require.NoError(t, err)
		assert.Contains(t, output.String(), "Release created:")
	})

	t.Run("update release without notes in non-interactive mode", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		okResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}

		// Tag exists
		testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

		// Release exists
		testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Release{
			Name:        "0.0.1",
			TagName:     "0.0.1",
			Description: "Original description",
			Links:       gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
		}, okResponse, nil)

		// Update release - verifying we can update without providing description
		testClient.MockReleases.EXPECT().UpdateRelease("OWNER/REPO", "0.0.1", gomock.Any()).
			DoAndReturn(func(pid any, tagName string, opts *gitlab.UpdateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
				// Description should not be set (or be empty) since we didn't provide notes
				if opts.Description != nil {
					assert.Empty(t, *opts.Description)
				}
				return &gitlab.Release{
					Name:        "0.0.1",
					TagName:     "0.0.1",
					Description: "Original description", // Keep original
					Links:       gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
				}, nil, nil
			})

		// Update without providing notes - just update the name
		output, err := exec("0.0.1 --name 'Updated Name'")
		require.NoError(t, err)
		assert.Contains(t, output.String(), "Release updated")
	})
}

func TestReleaseCreate_DefaultBranchDetectionForRef(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "")

	t.Run("use default branch from project API when available", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
		tc.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{DefaultBranch: "some-default-branch"}, nil, nil)
		tc.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
		tc.MockReleases.EXPECT().CreateRelease("OWNER/REPO", &gitlab.CreateReleaseOptions{
			Name:        new("0.0.1"),
			TagName:     new("0.0.1"),
			Description: new("test release"),
			Ref:         new("some-default-branch"),
		}).Return(&gitlab.Release{}, nil, nil)

		_, err := exec("0.0.1 --notes \"test release\"")
		require.NoError(t, err)
	})

	t.Run("use default branch from environment if available and project API not available", func(t *testing.T) {
		t.Setenv("CI_DEFAULT_BRANCH", "some-default-branch")

		tc := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
		tc.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(nil, nil, errors.New("forbidden"))
		tc.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
		tc.MockReleases.EXPECT().CreateRelease("OWNER/REPO", &gitlab.CreateReleaseOptions{
			Name:        new("0.0.1"),
			TagName:     new("0.0.1"),
			Description: new("test release"),
			Ref:         new("some-default-branch"),
		}).Return(&gitlab.Release{}, nil, nil)

		_, err := exec("0.0.1 --notes \"test release\"")
		require.NoError(t, err)
	})

	t.Run("no explicit ref if default branch not in environment and project API not available", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdCreate,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
		tc.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(nil, nil, errors.New("forbidden"))
		tc.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))
		tc.MockReleases.EXPECT().CreateRelease("OWNER/REPO", &gitlab.CreateReleaseOptions{
			Name:        new("0.0.1"),
			TagName:     new("0.0.1"),
			Description: new("test release"),
			Ref:         nil,
		}).Return(&gitlab.Release{}, nil, nil)

		_, err := exec("0.0.1 --notes \"test release\"")
		require.NoError(t, err)
	})
}

func TestReleaseCreate_UsePackageRegistryEnvVar(t *testing.T) {
	// NOTE: This test cannot use t.Parallel() because it uses t.Setenv().
	t.Setenv("CI_DEFAULT_BRANCH", "main")
	t.Setenv("GITLAB_RELEASE_ASSETS_USE_PACKAGE_REGISTRY", "true")

	t.Run("when env var is set, files are uploaded to the package registry instead of project markdown", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
		notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

		testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)
		testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

		// With the env var set, the file must go through the generic package registry,
		// not through UploadProjectMarkdown. If the bug is present (err != nil instead of
		// err == nil), usePackageRegistry stays false and UploadProjectMarkdown is called
		// instead — causing this expectation to fail as "expected call not received".
		testClient.MockGenericPackages.EXPECT().
			PublishPackageFile("OWNER/REPO", "release-assets", "0.0.1", "test_file.txt", gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&gitlab.GenericPackagesFile{}, nil, nil)

		testClient.MockGenericPackages.EXPECT().
			FormatPackageURL("OWNER/REPO", "release-assets", "0.0.1", "test_file.txt").
			Return("/api/v4/projects/OWNER/REPO/packages/generic/release-assets/0.0.1/test_file.txt", nil)

		testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).
			DoAndReturn(func(pid any, opts *gitlab.CreateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
				require.NotNil(t, opts.Assets)
				require.Len(t, opts.Assets.Links, 1)
				assert.Contains(t, *opts.Assets.Links[0].URL, "/packages/generic/release-assets/0.0.1/test_file.txt")

				return &gitlab.Release{
					Name:    "test_release",
					TagName: "0.0.1",
					Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
				}, nil, nil
			})

		exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec(`0.0.1 --notes "test release" testdata/test_file.txt`)

		require.NoError(t, err)
		assert.Contains(t, output.String(), "Release created:")
		assert.Empty(t, output.Stderr())
	})
}

func TestReleaseCreate_ExperimentalNotes(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "main")

	tests := []struct {
		name                string
		cli                 string
		files               map[string]string
		wantErr             bool
		errMsg              string
		setupMocks          bool
		expectedDescription string
	}{
		{
			name:       "when experimental notes is used with notes flag",
			cli:        `0.0.1 --experimental-notes-text-or-file "test.md" --notes "test"`,
			wantErr:    true,
			errMsg:     "if any flags in the group [experimental-notes-text-or-file notes] are set none of the others can be; [experimental-notes-text-or-file notes] were all set",
			setupMocks: false,
		},
		{
			name:       "when experimental notes is used with notes-file flag",
			cli:        `0.0.1 --experimental-notes-text-or-file "test.md" --notes-file "other.md"`,
			wantErr:    true,
			errMsg:     "if any flags in the group [experimental-notes-text-or-file notes-file] are set none of the others can be; [experimental-notes-text-or-file notes-file] were all set",
			setupMocks: false,
		},
		{
			name: "when experimental notes points to existing file",
			cli:  `0.0.1 --experimental-notes-text-or-file "test.md"`,
			files: map[string]string{
				"test.md": "# Test Release\nThis is a test release.",
			},
			setupMocks:          true,
			expectedDescription: "# Test Release\nThis is a test release.",
		},
		{
			name:                "when experimental notes has non-existent file and falls back to text",
			cli:                 `0.0.1 --experimental-notes-text-or-file "This is plain text"`,
			setupMocks:          true,
			expectedDescription: "This is plain text",
		},
		{
			name:                "when experimental notes contains spaces, treats as text",
			cli:                 `0.0.1 --experimental-notes-text-or-file "This contains spaces.md"`,
			setupMocks:          true,
			expectedDescription: "This contains spaces.md",
		},
		{
			name:                "when experimental notes has leading/trailing spaces",
			cli:                 `0.0.1 --experimental-notes-text-or-file " notes.md "`,
			setupMocks:          true,
			expectedDescription: " notes.md ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			for filename, content := range tt.files {
				err := os.WriteFile(filename, []byte(content), 0o600)
				require.NoError(t, err)
			}

			testClient := gitlabtesting.NewTestClient(t)

			if tt.setupMocks {
				notFoundResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}

				// Tag exists
				testClient.MockTags.EXPECT().GetTag("OWNER/REPO", "0.0.1", gomock.Any()).Return(&gitlab.Tag{Name: "0.0.1"}, nil, nil)

				// Release doesn't exist
				testClient.MockReleases.EXPECT().GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).Return(nil, notFoundResponse, errors.New("not found"))

				// Create release and verify description
				testClient.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateReleaseOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Release, *gitlab.Response, error) {
						if tt.expectedDescription != "" {
							require.NotNil(t, opts.Description)
							assert.Equal(t, tt.expectedDescription, *opts.Description)
						}
						return &gitlab.Release{
							Name:    "test_release",
							TagName: "0.0.1",
							Links:   gitlab.ReleaseLinks{Self: "https://gitlab.com/OWNER/REPO/-/releases/0.0.1"},
						}, nil, nil
					})
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tt.cli)

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				require.NoErrorf(t, err, "error running command `create %s`: %v", tt.cli, err)
				assert.Contains(t, output.String(), "✓ Release created:")
				assert.Empty(t, output.Stderr())
			}
		})
	}
}
