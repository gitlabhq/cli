//go:build !integration

package upload

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestReleaseUpload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cli  string

		wantType     bool
		expectedType gitlab.LinkTypeValue
	}{
		{
			name: "when a file is uploaded using filename only, and does not send a link_type",
			cli:  "0.0.1 testdata/test_file.txt",

			wantType: false,
		},
		{
			name: "when a file is uploaded using a filename, display name and type",
			cli:  "0.0.1 testdata/test_file.txt#test_file#other",

			wantType:     true,
			expectedType: gitlab.OtherLinkType,
		},
		{
			name: "when a file is uploaded using a filename and type only",
			cli:  "0.0.1 testdata/test_file.txt##package",

			wantType:     true,
			expectedType: gitlab.PackageLinkType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))

			// Mock GetRelease to validate the release exists
			testClient.MockReleases.EXPECT().
				GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).
				Return(&gitlab.Release{
					Name:    "test1",
					TagName: "0.0.1",
				}, nil, nil)

			// Mock UploadProjectMarkdown for file upload
			testClient.MockProjectMarkdownUploads.EXPECT().
				UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "test_file.txt", gomock.Any()).
				Return(&gitlab.MarkdownUploadedFile{
					Alt:      "test_file",
					URL:      "/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt",
					FullPath: "/namespace1/project1/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt",
					Markdown: "![test_file](/uploads/66dbcd21ec5d24ed6ea225176098d52b/test_file.txt)",
				}, nil, nil)

			// Mock CreateReleaseLink and verify link_type
			testClient.MockReleaseLinks.EXPECT().
				CreateReleaseLink("OWNER/REPO", "0.0.1", gomock.Any()).
				DoAndReturn(func(pid any, tagName string, opts *gitlab.CreateReleaseLinkOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ReleaseLink, *gitlab.Response, error) {
					if tc.wantType {
						require.NotNil(t, opts.LinkType)
						assert.Equal(t, tc.expectedType, *opts.LinkType)
					} else {
						assert.Nil(t, opts.LinkType)
					}

					return &gitlab.ReleaseLink{
						ID:             2,
						Name:           "test_file.txt",
						URL:            "https://gitlab.example.com/mynamespace/hello/-/jobs/688/artifacts/raw/testdata/test_file.txt",
						DirectAssetURL: "https://gitlab.example.com/mynamespace/hello/-/releases/0.0.1/downloads/testdata/test_file.txt",
						LinkType:       gitlab.OtherLinkType,
					}, nil, nil
				})

			exec := cmdtest.SetupCmdForTest(t, NewCmdUpload, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tc.cli)

			if assert.NoErrorf(t, err, "error running command `release upload %s`: %v", tc.cli, err) {
				assert.Contains(t, output.String(), `• Validating tag repo=OWNER/REPO tag=0.0.1
• Uploading release assets repo=OWNER/REPO tag=0.0.1
• Uploading to release	file=testdata/test_file.txt name=test_file.txt
✓ Upload succeeded after`)
				assert.Empty(t, output.Stderr())
			}
		})
	}
}

func TestReleaseUpload_WithAssetsLinksJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cli            string
		expectedOutput string
	}{
		{
			name: "with direct_asset_path",
			cli:  `0.0.1 --assets-links='[{"name": "any-name", "url": "https://example.com/any-asset-url", "direct_asset_path": "/any-path"}]'`,
			expectedOutput: `• Validating tag repo=OWNER/REPO tag=0.0.1
• Uploading release assets repo=OWNER/REPO tag=0.0.1
✓ Added release asset	name=any-name url=https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path
✓ Upload succeeded after`,
		},
		{
			name: "with filepath aliased to direct_asset_path",
			cli:  `0.0.1 --assets-links='[{"name": "any-name", "url": "https://example.com/any-asset-url", "filepath": "/any-path"}]'`,
			expectedOutput: `• Validating tag repo=OWNER/REPO tag=0.0.1
• Uploading release assets repo=OWNER/REPO tag=0.0.1
✓ Added release asset	name=any-name url=https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path
	! Aliased deprecated ` + "`filepath`" + ` field to ` + "`direct_asset_path`" + `. Replace ` + "`filepath`" + ` with ` + "`direct_asset_path`" + `	name=any-name
✓ Upload succeeded after`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))

			// Mock GetRelease to validate the release exists
			testClient.MockReleases.EXPECT().
				GetRelease("OWNER/REPO", "0.0.1", gomock.Any()).
				Return(&gitlab.Release{
					Name:    "test1",
					TagName: "0.0.1",
				}, nil, nil)

			// Mock CreateReleaseLink and verify the parameters
			testClient.MockReleaseLinks.EXPECT().
				CreateReleaseLink("OWNER/REPO", "0.0.1", gomock.Any()).
				DoAndReturn(func(pid any, tagName string, opts *gitlab.CreateReleaseLinkOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ReleaseLink, *gitlab.Response, error) {
					// Verify direct_asset_path is set and filepath is NOT set
					assert.NotNil(t, opts.DirectAssetPath)
					assert.Equal(t, "/any-path", *opts.DirectAssetPath)

					return &gitlab.ReleaseLink{
						ID:             1,
						Name:           "any-name",
						URL:            "https://example.com/any-asset-url",
						DirectAssetURL: "https://gitlab.example.com/OWNER/REPO/releases/0.0.1/downloads/any-path",
						LinkType:       gitlab.OtherLinkType,
					}, nil, nil
				})

			exec := cmdtest.SetupCmdForTest(t, NewCmdUpload, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tt.cli)

			if assert.NoErrorf(t, err, "error running command `release upload %s`: %v", tt.cli, err) {
				assert.Contains(t, output.String(), tt.expectedOutput)
				assert.Empty(t, output.Stderr())
			}
		})
	}
}
