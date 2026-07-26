package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/securefile/helpers"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func NewCmdDownload(f cmdutils.Factory) *cobra.Command {
	securefileDownloadCmd := &cobra.Command{
		Use:   "download [<id> | --id <id> | --name <name>] [flags]",
		Short: `Download one or more secure files from a project.`,
		Long: heredoc.Docf(`
		To download a single file, identify it by its numeric ID (as a positional
		argument or with %[1]s--id%[1]s) or by its name with %[1]s--name%[1]s. To download every
		secure file in the project, use %[1]s--all%[1]s.

		Use %[1]s--path%[1]s to save a single download to a specific filename, or
		%[1]s--output-dir%[1]s to choose the destination directory when downloading
		multiple files.

		By default, downloaded files are verified against their checksum.
		Use %[1]s--no-verify%[1]s to skip verification, or %[1]s--force-download%[1]s to keep
		files even when verification fails. Both options can allow corrupted
		or tampered files; use with caution.

		By default, files are downloaded from the current project. Use
		%[1]s--repo%[1]s to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Download a file by ID (positional or flag)
			glab securefile download 1
			glab securefile download --id 1

			# Download a file by ID to a specific path
			glab securefile download 1 --path "securefiles/file.txt"

			# Download a file by name to the current directory
			glab securefile download --name my-secure-file.pem

			# Download a file by name to a chosen path
			glab securefile download --name my-secure-file.pem --path securefiles/some-other-name.pem

			# Download without verifying the checksum
			glab securefile download 1 --no-verify

			# Download all secure files in the project
			glab securefile download --all

			# Download all secure files to a specific directory
			glab securefile download --all --output-dir secure_files/
		`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			noVerify, err := cmd.Flags().GetBool("no-verify")
			if err != nil {
				return fmt.Errorf("unable to get no-verify flag: %w", err)
			}

			forceDownload, err := cmd.Flags().GetBool("force-download")
			if err != nil {
				return fmt.Errorf("unable to get force-download flag: %w", err)
			}

			all, err := cmd.Flags().GetBool("all")
			if err != nil {
				return fmt.Errorf("unable to get all flag: %w", err)
			}

			root, err := os.OpenRoot(".")
			if err != nil {
				return fmt.Errorf("unable to open root directory: %w", err)
			}
			defer root.Close()

			if all {
				if len(args) > 0 && args[0] != "" {
					return errors.New("all flag is not compatible with arguments")
				}

				outputDir, err := cmd.Flags().GetString("output-dir")
				if err != nil {
					return fmt.Errorf("unable to get output-dir flag: %w", err)
				}

				return downloadAllSecureFiles(cmd.Context(), f.IO(), client, root, repo.FullName(), outputDir, !noVerify, forceDownload)
			} else {
				outputDirSet := cmd.Flags().Changed("output-dir")
				if outputDirSet {
					return errors.New("output-dir flag is only compatible with all flag")
				}

				path, err := cmd.Flags().GetString("path")
				if err != nil {
					return fmt.Errorf("unable to get path flag: %w", err)
				}

				// Download securefile by Name
				name, err := cmd.Flags().GetString("name")
				if err != nil {
					return fmt.Errorf("unable to get name flag: %w", err)
				}

				if name != "" {
					if len(args) > 0 && args[0] != "" {
						return errors.New("name flag is not compatible with arguments")
					}

					// If path wasn't explicitly set by user, use name as default
					if !cmd.Flags().Changed("path") {
						path = fmt.Sprintf("./%s", name)
					}

					return downloadSecureFileByName(cmd.Context(), f.IO(), client, root, name, repo.FullName(), path, !noVerify, forceDownload)
				}

				var fileID int64
				// Check if --id is passed, else drop to positional argument
				if cmd.Flags().Changed("id") {
					fileID, err = cmd.Flags().GetInt64("id")
					if err != nil {
						return fmt.Errorf("unable to get id flag: %w", err)
					}
				} else {
					// Guard against no args
					if len(args) == 0 {
						return errors.New("must provide fileID argument, --id or --name flag")
					}

					fileID, err = strconv.ParseInt(args[0], 10, 64)
					if err != nil {
						return fmt.Errorf("secure file ID must be an integer: %s", args[0])
					}
				}

				return downloadSecureFile(cmd.Context(), f.IO(), client, root, fileID, repo.FullName(), path, !noVerify, forceDownload)
			}
		},
	}
	securefileDownloadCmd.Flags().StringP("path", "p", "./downloaded.tmp", "Path to download the secure file to, including filename and extension.")
	securefileDownloadCmd.Flags().String("output-dir", ".", "Output directory for files downloaded with --all.")
	securefileDownloadCmd.Flags().Int64("id", 0, "ID of the secure file to download.")
	securefileDownloadCmd.Flags().String("name", "", "Name of the secure file to download. Saves the file with this name, or to the path specified by --path.")
	securefileDownloadCmd.Flags().Bool("no-verify", false, "Do not verify the checksum of the downloaded file(s). Warning: when enabled, this setting allows the download of files that are corrupt or tampered with.")
	securefileDownloadCmd.Flags().Bool("force-download", false, "Force download file(s) even if checksum verification fails. Warning: when enabled, this setting allows the download of files that are corrupt or tampered with.")
	securefileDownloadCmd.Flags().Bool("all", false, "Download all of a project's secure files. Files are downloaded with their original name and file extension.")

	securefileDownloadCmd.MarkFlagsMutuallyExclusive("no-verify", "force-download")
	securefileDownloadCmd.MarkFlagsMutuallyExclusive("path", "output-dir")
	securefileDownloadCmd.MarkFlagsMutuallyExclusive("path", "all")
	securefileDownloadCmd.MarkFlagsMutuallyExclusive("id", "name", "all")

	return securefileDownloadCmd
}

func downloadSecureFileByName(ctx context.Context, ios *iostreams.IOStreams, client *gitlab.Client, root *os.Root, fileName string, repoName, path string, verifyChecksum, forceDownload bool) error {
	path = filepath.Clean(path)
	if err := ensureDirectoryExists(root, path); err != nil {
		return err
	}

	secureFile, err := helpers.GetSecureFileByName(client, fileName, repoName)
	if err != nil {
		return err
	}

	err = saveFile(ctx, ios, client, repoName, secureFile.ID, path, verifyChecksum, forceDownload)
	if err != nil {
		return err
	}

	ios.LogInfof("Downloaded secure file '%s' (Name: %s)\n", filepath.Base(path), fileName)
	return nil
}

func downloadSecureFile(ctx context.Context, ios *iostreams.IOStreams, client *gitlab.Client, root *os.Root, fileID int64, repoName, path string, verifyChecksum, forceDownload bool) error {
	path = filepath.Clean(path)
	if err := ensureDirectoryExists(root, path); err != nil {
		return err
	}

	err := saveFile(ctx, ios, client, repoName, fileID, path, verifyChecksum, forceDownload)
	if err != nil {
		return err
	}

	ios.LogInfof("Downloaded secure file '%s' (ID: %d)\n", filepath.Base(path), fileID)
	return nil
}

func downloadAllSecureFiles(ctx context.Context, ios *iostreams.IOStreams, client *gitlab.Client, root *os.Root, repoName, outputDir string, verifyChecksum, forceDownload bool) error {
	for file, err := range gitlab.Scan2(func(p gitlab.PaginationOptionFunc) ([]*gitlab.SecureFile, *gitlab.Response, error) {
		return client.SecureFiles.ListProjectSecureFiles(repoName, nil, p, gitlab.WithContext(ctx))
	}) {
		if err != nil {
			return fmt.Errorf("error fetching secure files: %w", err)
		}

		filePath := filepath.Join(outputDir, file.Name)

		if err := downloadSecureFile(ctx, ios, client, root, file.ID, repoName, filePath, verifyChecksum, forceDownload); err != nil {
			return fmt.Errorf("error downloading secure file '%s' (ID: %d): %w", file.Name, file.ID, err)
		}
	}

	return nil
}

func saveFile(ctx context.Context, ios *iostreams.IOStreams, apiClient *gitlab.Client, repoName string, fileID int64, path string, verifyChecksum, forceDownload bool) (err error) {
	contents, _, err := apiClient.SecureFiles.DownloadSecureFile(repoName, fileID, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("error downloading secure file: %w", err)
	}

	root, err := os.OpenRoot(".")
	if err != nil {
		return fmt.Errorf("unable to open root directory: %w", err)
	}
	defer root.Close()

	dir := filepath.Dir(path)
	tempBase := filepath.Join(dir, strconv.FormatInt(fileID, 10))
	tempFile, err := utils.CreateTemp(root, tempBase)
	if err != nil {
		return fmt.Errorf("unable to create temporary file for downloaded secure file: %w", err)
	}

	defer func() {
		if closeErr := tempFile.Close(); closeErr != nil {
			closeErr = fmt.Errorf("error closing temporary file: %w", closeErr)
			err = errors.Join(err, closeErr)
		}
		if _, statErr := root.Stat(tempFile.Name()); statErr == nil { // Cleanup the temp file if it hasn't been renamed
			if removeErr := root.Remove(tempFile.Name()); removeErr != nil {
				removeErr = fmt.Errorf("error removing temporary file: %w", removeErr)
				err = errors.Join(err, removeErr)
			}
		}
	}()

	if verifyChecksum {
		file, _, err := apiClient.SecureFiles.ShowSecureFileDetails(repoName, fileID, gitlab.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("error getting secure file details: %w", err)
		}

		hasher := sha256.New()
		teeReader := io.TeeReader(contents, hasher)

		if _, err := io.Copy(tempFile, teeReader); err != nil {
			return fmt.Errorf("unable to write to temporary file for checksum verification: %w", err)
		}

		if checksum := hex.EncodeToString((hasher.Sum(nil))); checksum != file.Checksum {
			if forceDownload {
				ios.LogInfof("Checksum verification failed for %s: expected %s, got %s\n", file.Name, file.Checksum, checksum)
				ios.LogInfo("Force-download selected, continuing to download file.")
			} else {
				return fmt.Errorf("checksum verification failed for %s: expected %s, got %s", file.Name, file.Checksum, checksum)
			}
		}
	} else {
		if _, err := io.Copy(tempFile, contents); err != nil {
			return fmt.Errorf("unable to write to downloaded file: %w", err)
		}
	}

	if err := root.Rename(tempFile.Name(), path); err != nil {
		return fmt.Errorf("unable to persist downloaded file contents: %w", err)
	}

	return err
}

func ensureDirectoryExists(root *os.Root, path string) error {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("error creating directory: %w", err)
		}
	}

	return nil
}
