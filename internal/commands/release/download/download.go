package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils/upload"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	tagName    string
	assetNames []string
	dir        string

	io           *iostreams.IOStreams
	apiClient    func(repoHost string) (*api.Client, error)
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdDownload(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		apiClient:    f.ApiClient,
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "download [<tag>]",
		Short: "Download asset files from a GitLab release.",
		Long: heredoc.Docf(`Download asset files from a GitLab release.

			If no tag is specified, downloads assets from the latest release.
			To specify a file name to download from the release assets, use %[1]s--asset-name%[1]s.
			%[1]s--asset-name%[1]s flag accepts glob patterns.
		`, "`"),
		Args: cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# Download all assets from the latest release
			glab release download

			# Download all assets from the specified release tag
			glab release download v1.1.0

			# Download assets with names matching the glob pattern
			glab release download v1.10.1 --asset-name="*.tar.gz"`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().StringArrayVarP(&opts.assetNames, "asset-name", "n", []string{}, "Download only assets that match the name or a glob pattern.")
	cmd.Flags().StringVarP(&opts.dir, "dir", "D", ".", "Directory to download the release assets to.")

	return cmd
}

func (o *options) complete(args []string) {
	if len(args) == 1 {
		o.tagName = args[0]
	}
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}
	color := o.io.Color()
	var resp *gitlab.Response
	var release *gitlab.Release
	var downloadableAssets []*upload.ReleaseAsset

	if o.tagName == "" {
		o.io.LogInfof("%s fetching latest release %s=%s\n",
			color.ProgressIcon(),
			color.Blue("repo"), repo.FullName())
		releases, _, err := client.Releases.ListReleases(repo.FullName(), &gitlab.ListReleasesOptions{})
		if err != nil {
			return cmdutils.WrapError(err, "could not fetch latest release.")
		}
		if len(releases) < 1 {
			return cmdutils.WrapError(errors.New("not found"), fmt.Sprintf("no release found for %q", repo.FullName()))
		}

		release = releases[0]
		o.tagName = release.TagName
	} else {
		o.io.LogInfof("%s fetching release %s=%s %s=%s.\n",
			color.ProgressIcon(),
			color.Blue("repo"), repo.FullName(),
			color.Blue("tag"), o.tagName)

		release, resp, err = client.Releases.GetRelease(repo.FullName(), o.tagName)
		if err != nil {
			if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden) {
				return cmdutils.WrapError(err, "release does not exist.")
			}
			return cmdutils.WrapError(err, "failed to fetch release.")
		}
	}

	for _, link := range release.Assets.Links {
		if len(o.assetNames) > 0 && (!matchAny(o.assetNames, link.Name)) {
			continue
		}
		downloadableAssets = append(downloadableAssets, &upload.ReleaseAsset{
			Name: &link.Name,
			URL:  &link.URL,
		})
	}

	for _, source := range release.Assets.Sources {
		name := path.Base(source.URL)
		if len(o.assetNames) > 0 && (!matchAny(o.assetNames, name)) {
			continue
		}
		downloadableAssets = append(downloadableAssets, &upload.ReleaseAsset{
			Name: &name,
			URL:  &source.URL,
		})
	}

	if len(downloadableAssets) < 1 {
		o.io.LogInfof("%s no release assets found!\n",
			color.DotWarnIcon())
		return nil
	}
	o.io.LogInfof("%s downloading release assets %s=%s %s=%s\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("tag"), o.tagName)

	err = downloadAssets(ctx, client, o.io, downloadableAssets, o.dir)
	if err != nil {
		return cmdutils.WrapError(err, "failed to download release.")
	}

	o.io.LogInfof(color.Bold("%s release %q downloaded\n"), color.GreenCheck(), release.Name)

	return nil
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		matched, err := filepath.Match(p, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func downloadAssets(ctx context.Context, client *gitlab.Client, io *iostreams.IOStreams, toDownload []*upload.ReleaseAsset, destDir string) error {
	color := io.Color()
	for _, asset := range toDownload {
		io.LogInfof("%s downloading file %s=%s %s=%s.\n",
			color.ProgressIcon(),
			color.Blue("name"), *asset.Name,
			color.Blue("url"), *asset.URL)

		var sanitizedAssetName string
		if asset.Name != nil {
			sanitizedAssetName = sanitizeAssetName(*asset.Name)
		}
		destDir, err := filepath.Abs(destDir)
		if err != nil {
			return fmt.Errorf("resolving absolute download directory path: %w", err)
		}

		destPath := filepath.Join(destDir, sanitizedAssetName)
		if !strings.HasPrefix(destPath, destDir) {
			return fmt.Errorf("invalid file path name")
		}

		err = downloadAsset(ctx, client, *asset.URL, destPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func sanitizeAssetName(asset string) string {
	if !strings.HasPrefix(asset, "/") {
		// Prefix the asset with "/" ensures that filepath.Clean removes all `/..`
		// See rule 4 of filepath.Clean for more information: https://pkg.go.dev/path/filepath#Clean
		asset = "/" + asset
	}
	return filepath.Clean(asset)
}

func downloadAsset(ctx context.Context, client *gitlab.Client, assetURL, destinationPath string) (err error) {
	baseURL, err := url.Parse(assetURL)
	if err != nil {
		return fmt.Errorf("parsing release asset URL: %w", err)
	}

	f, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	// Close can report a failed buffered write, and a leftover file makes the O_EXCL retry fail with "file exists".
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			err = errors.Join(err, os.Remove(destinationPath))
		}
	}()

	// check if authenticated GitLab client should be used or not.
	gitlabBaseURL := client.BaseURL()
	if gitlabBaseURL.Scheme == baseURL.Scheme && gitlabBaseURL.Host == baseURL.Host {
		// `glab release create` stores an uploaded file as a project Markdown
		// upload, and the link it saves is a web route. Only a browser session
		// authenticates that route, so a token request is redirected to the
		// sign-in page, which answers 404 for a private project. The uploads
		// API serves the same file and accepts the token.
		if upload, ok := parseMarkdownUploadRef(gitlabBaseURL, baseURL); ok {
			return downloadMarkdownUpload(ctx, client, upload, f)
		}

		r, err := client.NewRequestToURL(http.MethodGet, baseURL, http.NoBody, []gitlab.RequestOptionFunc{
			gitlab.WithContext(ctx),
			gitlab.WithHeader("Accept", "application/octet-stream"),
		})
		if err != nil {
			return err
		}
		_, err = client.Do(r, f)
		return err
	} else {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL.String(), http.NoBody)
		if err != nil {
			return err
		}
		r.Header.Add("Accept", "application/octet-stream")

		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode > 299 {
			return errors.New(resp.Status)
		}
		_, err = io.Copy(f, resp.Body)
		return err
	}
}

// markdownUploadRef identifies a project Markdown upload behind a release
// asset link.
type markdownUploadRef struct {
	project  string // numeric project ID, or a namespace and project path
	secret   string
	filename string
}

// parseMarkdownUploadRef recognizes the two link shapes GitLab stores for a
// project Markdown upload:
//
//	/-/project/<project_id>/uploads/<secret>/<filename>  GitLab 17.0 and later
//	/<namespace>/<project>/uploads/<secret>/<filename>   earlier versions
//
// Any other link returns false, which keeps package registry links, release
// evidence, and source archives on their existing download path.
func parseMarkdownUploadRef(baseURL, assetURL *url.URL) (markdownUploadRef, bool) {
	assetPath := strings.TrimPrefix(assetURL.EscapedPath(), instanceBasePath(baseURL))
	segments := strings.Split(strings.Trim(assetPath, "/"), "/")

	// The shortest match is <namespace>/<project>/uploads/<secret>/<filename>.
	if len(segments) < 5 {
		return markdownUploadRef{}, false
	}

	uploadsAt := len(segments) - 3
	if segments[uploadsAt] != "uploads" {
		return markdownUploadRef{}, false
	}

	secret, err := url.PathUnescape(segments[uploadsAt+1])
	if err != nil || secret == "" {
		return markdownUploadRef{}, false
	}

	filename, err := url.PathUnescape(segments[uploadsAt+2])
	if err != nil || filename == "" {
		return markdownUploadRef{}, false
	}

	project := segments[:uploadsAt]
	if len(project) == 3 && project[0] == "-" && project[1] == "project" {
		return markdownUploadRef{project: project[2], secret: secret, filename: filename}, true
	}

	// A project always sits in a namespace, so a single segment cannot be one.
	// GitLab reserves `-` as a path name: it ends the project path and starts
	// a project route, such as /-/raw/ or /-/blob/, which is not an upload.
	if len(project) < 2 || slices.Contains(project, "-") {
		return markdownUploadRef{}, false
	}
	return markdownUploadRef{project: strings.Join(project, "/"), secret: secret, filename: filename}, true
}

// instanceBasePath returns the path prefix of an instance that is served from
// a subfolder. client.BaseURL points at the API root, so the API segments are
// removed to leave that prefix.
func instanceBasePath(baseURL *url.URL) string {
	return strings.TrimSuffix(strings.TrimSuffix(baseURL.EscapedPath(), "/"), "/api/v4")
}

func downloadMarkdownUpload(ctx context.Context, client *gitlab.Client, ref markdownUploadRef, w io.Writer) error {
	body, _, err := client.ProjectMarkdownUploads.DownloadProjectMarkdownUploadBySecretAndFilename(
		ref.project, ref.secret, ref.filename, gitlab.WithContext(ctx))
	if err != nil {
		return err
	}
	defer body.Close()

	_, err = io.Copy(w, body)
	return err
}
