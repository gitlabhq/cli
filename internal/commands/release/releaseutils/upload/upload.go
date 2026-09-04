package upload

import (
	"fmt"
	"io"
	"net/url"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// DefaultReleasePackageName contains the name for the release asset package
const DefaultReleasePackageName = "release-assets"

// ConflictDirectAssetPathError is returned when both direct_asset_path and the deprecated filepath as specified for an asset link.
type ConflictDirectAssetPathError struct {
	assetLinkName *string
}

func (e *ConflictDirectAssetPathError) Error() string {
	var name string
	if e.assetLinkName != nil {
		name = *e.assetLinkName
	} else {
		name = "(without a name)"
	}
	return fmt.Sprintf("asset link %s contains both `direct_asset_path` and `filepath` (deprecated) fields. Remove the deprecated `filepath` field.", name)
}

type ReleaseAsset struct {
	Name *string `json:"name,omitempty"`
	URL  *string `json:"url,omitempty"`
	// Deprecated FilePath use DirectAssetPath instead.
	FilePath        *string               `json:"filepath,omitempty"`
	DirectAssetPath *string               `json:"direct_asset_path,omitempty"`
	LinkType        *gitlab.LinkTypeValue `json:"link_type,omitempty"`
}

type ReleaseFile struct {
	Open  func() (io.ReadCloser, error)
	Name  string
	Label string
	Path  string
	Type  *gitlab.LinkTypeValue
}

func CreateLink(c *gitlab.Client, projectID, tagName string, asset *ReleaseAsset) (*gitlab.ReleaseLink, bool /* aliased deprecated filepath */, error) {
	aliased, err := aliasFilePathToDirectAssetPath(asset)
	if err != nil {
		return nil, false, err
	}

	releaseLink, _, err := c.ReleaseLinks.CreateReleaseLink(projectID, tagName, &gitlab.CreateReleaseLinkOptions{
		Name:            asset.Name,
		URL:             asset.URL,
		DirectAssetPath: asset.DirectAssetPath,
		LinkType:        asset.LinkType,
	})
	if err != nil {
		return nil, false, err
	}

	return releaseLink, aliased, nil
}

type Context struct {
	Client      *gitlab.Client
	IO          *iostreams.IOStreams
	AssetFiles  []*ReleaseFile
	AssetsLinks []*ReleaseAsset
}

// UploadFiles uploads a file into a release repository.
func (c *Context) UploadFiles(projectID, tagName string, packageName string, usePackageRegistry bool) error {
	if c.AssetFiles == nil {
		return nil
	}
	color := c.IO.Color()
	for _, file := range c.AssetFiles {
		c.IO.LogInfof("%s Uploading to release\t%s=%s %s=%s\n",
			color.ProgressIcon(), color.Blue("file"), file.Path,
			color.Blue("name"), file.Name)

		var releaseAsset *ReleaseAsset
		var err error
		if usePackageRegistry {
			releaseAsset, err = c.uploadAsGenericPackage(projectID, tagName, packageName, file)
		} else {
			releaseAsset, err = c.uploadAsProjectMarkdownFile(projectID, file)
		}
		if err != nil {
			return err
		}

		_, _, err = CreateLink(c.Client, projectID, tagName, releaseAsset)
		if err != nil {
			return err
		}
	}
	c.AssetFiles = nil

	return nil
}

func (c *Context) uploadAsGenericPackage(projectID, tagName string, packageName string, file *ReleaseFile) (*ReleaseAsset, error) {
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	_, _, err = c.Client.GenericPackages.PublishPackageFile(projectID, packageName, tagName, file.Name, r, nil)
	if err != nil {
		return nil, err
	}

	assetPath, err := c.Client.GenericPackages.FormatPackageURL(projectID, packageName, tagName, file.Name)
	if err != nil {
		return nil, err
	}
	assetURL := c.Client.BaseURL().JoinPath(assetPath)
	return &ReleaseAsset{
		Name:            new(file.Label),
		URL:             new(assetURL.String()),
		DirectAssetPath: new("/" + url.PathEscape(file.Name)),
		LinkType:        file.Type,
	}, nil
}

func (c *Context) uploadAsProjectMarkdownFile(projectID string, file *ReleaseFile) (*ReleaseAsset, error) {
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	projectFile, _, err := c.Client.ProjectMarkdownUploads.UploadProjectMarkdown(
		projectID,
		r,
		file.Name,
		nil,
	)
	if err != nil {
		return nil, err
	}

	// projectFile.FullPath contains the absolute path of the file within the
	// context of the client's assetURL.
	//
	// It has a structure like so:
	// FullPath: /-/project/[project_id]/uploads/[hash]/[file]
	// Or on older GitLab prior to 17:
	// FullPath: /[namespace]/[project]/uploads/[hash]/[file]
	//
	// Thus, we append it to the base URL to get a usable link.
	//
	// See for context https://docs.gitlab.com/api/project_markdown_uploads/
	assetURL := c.Client.BaseURL()
	assetURL.Path = projectFile.FullPath

	return &ReleaseAsset{
		Name:            new(file.Label),
		URL:             new(assetURL.String()),
		DirectAssetPath: new("/" + url.PathEscape(file.Name)),
		LinkType:        file.Type,
	}, nil
}

func (c *Context) CreateReleaseAssetLinks(projectID string, tagName string) error {
	if c.AssetsLinks == nil {
		return nil
	}
	color := c.IO.Color()
	for _, asset := range c.AssetsLinks {
		releaseLink, aliased, err := CreateLink(c.Client, projectID, tagName, asset)
		if err != nil {
			return err
		}
		c.IO.LogInfof("%s Added release asset\t%s=%s %s=%s\n",
			color.GreenCheck(), color.Blue("name"), *asset.Name,
			color.Blue("url"), releaseLink.DirectAssetURL)

		if aliased {
			c.IO.LogInfof("\t%s Aliased deprecated `filepath` field to `direct_asset_path`. Replace `filepath` with `direct_asset_path`\t%s=%s\n",
				color.WarnIcon(),
				color.Blue("name"), *asset.Name)
		}

	}
	c.AssetsLinks = nil

	return nil
}

// aliasFilePathToDirectAssetPath ensures that the Asset Link uses the direct_asset_path and not the filepath.
// The filepath is deprecated and will be fully removed in GitLab 17.0.
// See https://gitlab.com/groups/gitlab-org/-/work_items/9661
func aliasFilePathToDirectAssetPath(asset *ReleaseAsset) (bool /* aliased */, error) {
	if asset.FilePath == nil || *asset.FilePath == "" {
		// There is no deprecated filepath set, so we are good.
		return false, nil
	}

	if asset.DirectAssetPath != nil && *asset.DirectAssetPath != "" {
		// Both, filepath and direct_asset_path are set, so we have a conflict
		return false, &ConflictDirectAssetPathError{asset.Name}
	}

	// We use the set filepath as direct_asset_path
	// and clear the filepath to net send it via API.
	asset.DirectAssetPath = asset.FilePath
	asset.FilePath = nil

	return true, nil
}
