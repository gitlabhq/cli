//go:build !integration

package upload

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestReleaseUtilsUpload_AliasFilePathToAssetDirectPath(t *testing.T) {
	tests := []struct {
		name                 string
		givenReleaseAsset    *ReleaseAsset
		expectedReleaseAsset *ReleaseAsset
		expectedAliased      bool
	}{
		{
			name:                 "no filepath, no direct_asset_path",
			givenReleaseAsset:    &ReleaseAsset{Name: new("any-name")},
			expectedReleaseAsset: &ReleaseAsset{Name: new("any-name")},
			expectedAliased:      false,
		},
		{
			name:                 "no filepath, but direct_asset_path",
			givenReleaseAsset:    &ReleaseAsset{Name: new("any-name"), DirectAssetPath: new("/any-path")},
			expectedReleaseAsset: &ReleaseAsset{Name: new("any-name"), DirectAssetPath: new("/any-path")},
			expectedAliased:      false,
		},
		{
			name:                 "filepath, but not direct_asset_path",
			givenReleaseAsset:    &ReleaseAsset{Name: new("any-name"), FilePath: new("/any-path")},
			expectedReleaseAsset: &ReleaseAsset{Name: new("any-name"), DirectAssetPath: new("/any-path")},
			expectedAliased:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aliased, err := aliasFilePathToDirectAssetPath(tt.givenReleaseAsset)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedAliased, aliased)
			assert.Equal(t, tt.expectedReleaseAsset, tt.givenReleaseAsset)
		})
	}
}

func TestReleaseUtilsUpload_AliasFilePathToAssetDirectPath_Conflict(t *testing.T) {
	asset := &ReleaseAsset{
		FilePath:        new("/any-path"),
		DirectAssetPath: new("/any-path"),
	}

	aliased, err := aliasFilePathToDirectAssetPath(asset)

	target := &ConflictDirectAssetPathError{}
	require.ErrorAs(t, err, &target)
	assert.False(t, aliased)
}

func TestReleaseUtilsUpload_UploadFiles_ProjectMarkdownFiles(t *testing.T) {
	t.Parallel()

	// GIVEN
	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	testReleaseFile := bytes.NewBufferString("Hello World")
	uploadCtx := &Context{
		Client: tc.Client,
		IO:     ios,
		AssetFiles: []*ReleaseFile{
			{
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(testReleaseFile), nil
				},
				Name: "test-release-file.txt",
				Path: "./foobar/test-release-file.txt",
				Type: new(gitlab.OtherLinkType),
			},
		},
		AssetsLinks: []*ReleaseAsset{},
	}

	// setup mock exceptions
	gomock.InOrder(
		tc.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("any-project", gomock.Any(), "test-release-file.txt", gomock.Any()).
			Return(&gitlab.MarkdownUploadedFile{FullPath: "test-release-file.txt"}, nil, nil),
		tc.MockReleaseLinks.EXPECT().CreateReleaseLink("any-project", "42.0.0", gomock.Any()),
	)

	// WHEN
	err := uploadCtx.UploadFiles("any-project", "42.0.0", DefaultReleasePackageName, false)

	// THEN
	require.NoError(t, err)
}

func TestReleaseUtilsUpload_UploadFiles_GenericPackageRegistry(t *testing.T) {
	t.Parallel()

	// GIVEN
	ios, _, _, _ := cmdtest.TestIOStreams()
	tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
	testReleaseFile := bytes.NewBufferString("Hello World")
	uploadCtx := &Context{
		Client: tc.Client,
		IO:     ios,
		AssetFiles: []*ReleaseFile{
			{
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(testReleaseFile), nil
				},
				Name: "test-release-file.txt",
				Path: "./foobar/test-release-file.txt",
				Type: new(gitlab.OtherLinkType),
			},
		},
		AssetsLinks: []*ReleaseAsset{},
	}

	// setup mock exceptions
	gomock.InOrder(
		tc.MockGenericPackages.EXPECT().
			PublishPackageFile("any-project", DefaultReleasePackageName, "42.0.0", "test-release-file.txt", gomock.Any(), nil),
		tc.MockGenericPackages.EXPECT().
			FormatPackageURL("any-project", DefaultReleasePackageName, "42.0.0", "test-release-file.txt"),
		tc.MockReleaseLinks.EXPECT().CreateReleaseLink("any-project", "42.0.0", gomock.Any()),
	)

	// WHEN
	err := uploadCtx.UploadFiles("any-project", "42.0.0", DefaultReleasePackageName, true)

	// THEN
	require.NoError(t, err)
}
