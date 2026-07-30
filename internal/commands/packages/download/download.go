package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

const genericPackageType = "generic"

type options struct {
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)

	packageName    string
	packageVersion string
	fileName       string
	path           string
	verifyChecksum bool
	forceDownload  bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "download --name <package> --version <version> --filename <file> [flags]",
		Short: `Download a file from a project's package registry.`,
		Long: heredoc.Docf(`
		Download a generic package file, identified by its package name, version,
		and file name.

		Use %[1]s--path%[1]s to choose where the file is saved. If it names a directory
		(it ends with a separator or already exists as one), the file is saved
		there under its original name. Otherwise it is saved as that exact path,
		letting you rename the file. By default the file is saved in the current
		directory under its original name.

		By default, the downloaded file is verified against the checksum stored in
		the registry. This requires extra API calls to look up the file's metadata.
		Use %[1]s--no-verify%[1]s to skip verification. This can allow corrupted or
		tampered files; use with caution.

		If the target file already exists, the download fails. Use %[1]s--force%[1]s to
		overwrite it.

		By default, files are downloaded from the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Download a package file to the current directory
			glab packages download --name my-package --version 1.0.0 --filename app.zip

			# Download into a directory, keeping the original file name
			glab packages download -n my-package --version 1.0.0 --filename app.zip --path ./downloads/

			# Download to an exact path, renaming the file
			glab packages download -n my-package --version 1.0.0 --filename app.zip --path ./downloads/renamed.zip

			# Download to an absolute path
			glab packages download -n my-package --version 1.0.0 --filename app.zip --path /tmp/downloads/app.zip

			# Download without verifying the checksum
			glab packages download -n my-package --version 1.0.0 --filename app.zip --no-verify

			# Overwrite an existing file
			glab packages download -n my-package --version 1.0.0 --filename app.zip --force

			# Use the 'dl' alias and target another project
			glab packages dl -n my-package --version 1.0.0 --filename app.zip -R owner/repo
		`),
		Aliases: []string{"dl"},
		Args:    cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringP("name", "n", "", "Name of the package.")
	fl.String("version", "", "Version of the package.")
	fl.String("filename", "", "Name of the file within the package to download.")
	fl.StringP("path", "p", "", "Directory to save the file in (keeps its original name) or a full file path to rename it. Defaults to the original name in the current directory.")
	fl.Bool("no-verify", false, "Do not verify the checksum of the downloaded file. Warning: when enabled, this setting allows the download of files that are corrupt or tampered with.")
	fl.Bool("force", false, "Overwrite the target file if it already exists.")

	cobra.CheckErr(cmd.MarkFlagRequired("name"))
	cobra.CheckErr(cmd.MarkFlagRequired("version"))
	cobra.CheckErr(cmd.MarkFlagRequired("filename"))

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
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

	noVerify, err := fl.GetBool("no-verify")
	if err != nil {
		return err
	}
	o.verifyChecksum = !noVerify

	force, err := fl.GetBool("force")
	if err != nil {
		return err
	}
	o.forceDownload = force

	path, err := fl.GetString("path")
	if err != nil {
		return err
	}
	switch {
	case !fl.Changed("path"):
		path = o.fileName
	case isDirectoryPath(path):
		path = filepath.Join(path, o.fileName)
	}
	o.path = filepath.Clean(path)

	return nil
}

// isDirectoryPath reports whether path points at a directory to place the file
// in, rather than the file's full destination. A trailing separator or an
// existing directory both signal this, mirroring how `mv` treats its target.
func isDirectoryPath(path string) bool {
	if path == "" {
		return false
	}
	if os.IsPathSeparator(path[len(path)-1]) {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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

	root, name, err := utils.EnsureDestinationRoot(o.path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()

	if _, err := root.Stat(name); err == nil && !o.forceDownload {
		return fmt.Errorf("file %s already exists; use --force to overwrite current file", o.path)
	}

	if err := o.saveFile(ctx, client, root, name, repo.FullName()); err != nil {
		return err
	}

	o.io.LogInfof("Downloaded package file to '%s' (Package: %s, Version: %s)\n", o.path, o.packageName, o.packageVersion)
	return nil
}

func (o *options) saveFile(ctx context.Context, client *gitlab.Client, root *os.Root, name, repoName string) (err error) {
	contents, _, err := client.GenericPackages.DownloadPackageFile(repoName, o.packageName, o.packageVersion, o.fileName, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to download package file: %w", err)
	}

	var expected string
	if o.verifyChecksum {
		expected, err = fetchChecksum(ctx, client, repoName, o.packageName, o.packageVersion, o.fileName)
		if err != nil {
			return err
		}
	}

	tempFile, err := utils.CreateTemp(root, name)
	if err != nil {
		return fmt.Errorf("unable to create temporary file for downloaded package file: %w", err)
	}
	// root.OpenFile reports the name joined onto the root, which root's own
	// methods reject as escaping when the root is absolute. The temporary file
	// is always a direct child of root, so its base name addresses it.
	tempName := filepath.Base(tempFile.Name())

	defer func() {
		if closeErr := tempFile.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("error closing temporary file: %w", closeErr))
		}
		if _, statErr := root.Stat(tempName); statErr == nil { // Cleanup the temp file if it hasn't been renamed
			if removeErr := root.Remove(tempName); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("error removing temporary file: %w", removeErr))
			}
		}
	}()

	hasher := sha256.New()
	var w io.Writer = tempFile
	if o.verifyChecksum {
		w = io.MultiWriter(tempFile, hasher)
	}

	if _, err := io.Copy(w, bytes.NewReader(contents)); err != nil {
		return fmt.Errorf("unable to write downloaded file: %w", err)
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))
	if o.verifyChecksum && checksum != expected {
		return fmt.Errorf("checksum verification failed for %s: expected %s, got %s", o.fileName, expected, checksum)
	}

	if err := root.Rename(tempName, name); err != nil {
		return fmt.Errorf("unable to persist downloaded file contents: %w", err)
	}

	return err
}

// fetchChecksum looks up the SHA256 stored in the registry for the given file,
// since DownloadPackageFile returns no metadata. The package name and version
// are filtered server-side; the lookup paginates in case many packages or
// files share the filter.
func fetchChecksum(ctx context.Context, client *gitlab.Client, repoName, name, version, fileName string) (string, error) {
	packageType := genericPackageType
	packages, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Package, *gitlab.Response, error) {
		return client.Packages.ListProjectPackages(repoName, &gitlab.ListProjectPackagesOptions{
			PackageType:    &packageType,
			PackageName:    &name,
			PackageVersion: &version,
		}, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return "", fmt.Errorf("failed to look up package: %w", err)
	}

	var packageID int64
	found := false
	for _, pkg := range packages {
		if pkg.Name == name && pkg.Version == version {
			packageID = pkg.ID
			found = true
			break
		}
	}

	if !found {
		return "", fmt.Errorf("couldn't locate package %s version %s", name, version)
	}

	files, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.PackageFile, *gitlab.Response, error) {
		return client.Packages.ListPackageFiles(repoName, packageID, &gitlab.ListPackageFilesOptions{}, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return "", fmt.Errorf("failed to look up package files: %w", err)
	}

	for _, file := range files {
		if file.FileName == fileName {
			return file.FileSHA256, nil
		}
	}

	return "", fmt.Errorf("couldn't locate file %s in package %s version %s", fileName, name, version)
}
