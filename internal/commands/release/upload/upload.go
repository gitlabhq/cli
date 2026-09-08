package upload

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils"
	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils/upload"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	tagName          string
	assetLinksAsJSON string

	assetLinks []*upload.ReleaseAsset
	assetFiles []*upload.ReleaseFile

	usePackageRegistry bool
	packageName        string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdUpload(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "upload <tag> [<files>...]",
		Short: "Upload release asset files or links to a GitLab release.",
		Long: heredoc.Doc(`Upload release assets to a GitLab release.

		Define the display name by appending '#' after the filename.
		The link type comes after the display name, like this: 'myfile.tar.gz#My display name#package'
		`),
		Args: func() cobra.PositionalArgs {
			return func(cmd *cobra.Command, args []string) error {
				if len(args) < 1 {
					return &cmdutils.FlagError{Err: errors.New("no tag name provided")}
				}
				if len(args) < 2 && opts.assetLinksAsJSON == "" {
					return cmdutils.FlagError{Err: errors.New("no files specified")}
				}
				return nil
			}
		}(),
		Example: heredoc.Doc(`
			# Upload a release asset with a display name. 'Type' defaults to 'other'.
			glab release upload v1.0.1 '/path/to/asset.zip#My display label'

			# Upload a release asset with a display name and type.
			glab release upload v1.0.1 '/path/to/asset.png#My display label#image'

			# Upload all assets in a specified folder. 'Type' defaults to 'other'.
			glab release upload v1.0.1 ./dist/*

			# Upload all tarballs in a specified folder. 'Type' defaults to 'other'.
			glab release upload v1.0.1 ./dist/*.tar.gz

			# Upload release assets links specified as JSON string
			glab release upload v1.0.1 --assets-links='
			[
			  {
			    "name": "Asset1",
			    "url":"https://<domain>/some/location/1",
			    "link_type": "other",
			    "direct_asset_path": "path/to/file"
			  }
			]'`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd.Flags(), args); err != nil {
				return err
			}

			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run()
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.assetLinksAsJSON, "assets-links", "a", "", "`JSON` string representation of assets links, like: `--assets-links='[{\"name\": \"Asset1\", \"url\":\"https://<domain>/some/location/1\", \"link_type\": \"other\", \"direct_asset_path\": \"path/to/file\"}]'`.")
	fl.BoolVar(&opts.usePackageRegistry, "use-package-registry", false, "Upload release assets to the generic package registry of the project. Alternatively to this flag you may also set the GITLAB_RELEASE_ASSETS_USE_PACKAGE_REGISTRY environment variable to either the value true or 1. The flag takes precedence over this environment variable.")
	fl.StringVar(&opts.packageName, "package-name", upload.DefaultReleasePackageName, "The package name to use when uploading the assets to the generic package release with --use-package-registry.")

	return cmd
}

func (o *options) complete(flags *pflag.FlagSet, args []string) error {
	o.tagName = args[0]

	assetFiles, err := releaseutils.AssetsFromArgs(args[1:])
	if err != nil {
		return err
	}
	o.assetFiles = assetFiles

	if !flags.Changed("use-package-registry") {
		if usePackageRegistry, err := strconv.ParseBool(os.Getenv("GITLAB_RELEASE_ASSETS_USE_PACKAGE_REGISTRY")); err == nil {
			o.usePackageRegistry = usePackageRegistry
		}
	}

	return nil
}

func (o *options) validate() error {
	if o.assetFiles == nil && o.assetLinksAsJSON == "" {
		return cmdutils.FlagError{Err: errors.New("no files specified")}
	}

	if o.assetLinksAsJSON != "" {
		err := json.Unmarshal([]byte(o.assetLinksAsJSON), &o.assetLinks)
		if err != nil {
			return fmt.Errorf("failed to parse JSON string: %w", err)
		}
	}

	return nil
}

func (o *options) run() error {
	start := time.Now()

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

	o.io.LogInfof("%s Validating tag %s=%s %s=%s\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("tag"), o.tagName)

	release, resp, err := client.Releases.GetRelease(repo.FullName(), o.tagName)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden) {
			return cmdutils.WrapError(err, "release does not exist. Create a new release with `glab release create "+o.tagName+"`.")
		}
		return cmdutils.WrapError(err, "failed to fetch release.")
	}

	// upload files and create asset links
	err = releaseutils.CreateReleaseAssets(o.io, client, o.assetFiles, o.assetLinks, repo.FullName(), release.TagName, o.packageName, o.usePackageRegistry)
	if err != nil {
		return cmdutils.WrapError(err, "creating release assets failed.")
	}

	o.io.LogInfof(color.Bold("%s Upload succeeded after %0.2fs.\n"), color.GreenCheck(), time.Since(start).Seconds())
	return nil
}
