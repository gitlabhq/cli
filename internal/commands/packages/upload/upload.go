package upload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	packageName    string
	packageVersion string
	fileName       string
	inputFilePath  string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "upload <file> --name <package> --version <version> [flags]",
		Short: `Upload a file to a project's package registry.`,
		Long: heredoc.Docf(`
		Uploaded files are stored as generic packages: arbitrary files identified
		by a package name, version, and file name.

		The file is stored under the given package name and version. By default
		it keeps its original file name; use %[1]s--filename%[1]s to store it under a
		different name.

		By default, the file is uploaded to the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Upload a file as version 1.0.0 of package 'my-package'
			glab packages upload ./build/app.zip --name my-package --version 1.0.0

			# Store the file under a different name
			glab packages upload ./build/app.zip --name my-package --version 1.0.0 --filename release.zip

			# Use the 'ul' alias and upload to another project
			glab packages ul ./build/app.zip -n my-package --version 1.0.0 -R owner/repo
		`),
		Aliases: []string{"ul"},
		Args:    cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringP("name", "n", "", "Name of the package.")
	fl.StringP("version", "v", "", "Version of the package.")
	fl.String("filename", "", "Name to store the file under. Defaults to the local file name.")
	cobra.CheckErr(cmd.MarkFlagRequired("name"))
	cobra.CheckErr(cmd.MarkFlagRequired("version"))

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	fl := cmd.Flags()

	name, err := fl.GetString("name")
	if err != nil {
		return err
	}
	o.packageName = name

	version, err := fl.GetString("version")
	if err != nil {
		return err
	}
	o.packageVersion = version

	fileName, err := fl.GetString("filename")
	if err != nil {
		return err
	}
	o.fileName = fileName

	o.inputFilePath = args[0]
	if o.fileName == "" {
		o.fileName = filepath.Base(o.inputFilePath)
	}

	return nil
}

func (o *options) run(ctx context.Context) (err error) {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	reader, err := os.Open(o.inputFilePath)
	if err != nil {
		return fmt.Errorf("unable to read file at %s: %w", o.inputFilePath, err)
	}
	defer func() { err = errors.Join(err, reader.Close()) }()

	color := o.io.Color()
	o.io.LogInfof("%s Uploading package file %s=%s %s=%s %s=%s %s=%s\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("package"), o.packageName,
		color.Blue("version"), o.packageVersion,
		color.Blue("file"), o.fileName)

	file, _, err := client.GenericPackages.PublishPackageFile(repo.FullName(), o.packageName, o.packageVersion, o.fileName, reader, nil, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to upload package file: %w", err)
	}

	assetPath, err := client.GenericPackages.FormatPackageURL(repo.FullName(), o.packageName, o.packageVersion, o.fileName)
	if err != nil {
		return fmt.Errorf("failed to format package URL: %w", err)
	}
	assetURL := client.BaseURL().JoinPath(assetPath)

	o.io.LogInfof(color.Bold("%s Package file %s uploaded.\n"), color.GreenCheck(), o.fileName)
	o.io.LogInfof("  %s %s\n", color.Blue("url"), o.io.Hyperlink(assetURL.String(), assetURL.String()))
	o.io.LogInfof("  %s %s\n", color.Blue("sha256"), file.FileSHA256)
	return nil
}
