package artifact

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

const (
	// Read limit is 4GB
	defaultZIPReadLimit int64 = 4 * 1024 * 1024 * 1024
	defaultZIPFileLimit int   = 100000
)

func ensurePathIsCreated(filename string) error {
	dir, _ := filepath.Split(filename)

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0o700) // Create your file
		if err != nil {
			return fmt.Errorf("could not create new path: %w", err)
		}
	}
	return nil
}

func readZip(artifact *bytes.Reader, path string, listPaths bool, zipReadLimit int64, zipFileLimit int, out io.Writer) error {
	zipReader, err := zip.NewReader(artifact, artifact.Size())
	if err != nil {
		return err
	}

	if !config.CheckPathExists(path) {
		if err := os.Mkdir(path, 0o755); err != nil {
			return err
		}
	}

	if !strings.HasSuffix(path, "/") {
		path = path + "/"
	}

	var written int64 = 0
	if len(zipReader.File) > zipFileLimit {
		return fmt.Errorf("zip archive includes too many files: limit is %d files", zipFileLimit)
	}

	for _, v := range zipReader.File {
		sanitizedAssetName := utils.SanitizePathName(v.Name)

		destDir, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolving absolute download directory path: %w", err)
		}
		destPath := filepath.Join(destDir, sanitizedAssetName)
		if !strings.HasPrefix(destPath, destDir) {
			return fmt.Errorf("invalid file path name")
		}

		dbg.Debug("Writing:", destPath)

		if v.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, v.Mode()); err != nil {
				return err
			}
		} else {
			srcFile, err := zipReader.Open(v.Name)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			limitedReader := io.LimitReader(srcFile, zipReadLimit)

			err = ensurePathIsCreated(destPath)
			if err != nil {
				return err
			}

			symlinkCheck, _ := os.Lstat(destPath)

			if symlinkCheck != nil && symlinkCheck.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("can't extract: a file in the artifact would overwrite a symbolic link")
			}

			dstFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, v.Mode())
			if err != nil {
				return err
			}
			defer dstFile.Close()

			var writtenPerFile int64
			if writtenPerFile, err = io.Copy(dstFile, limitedReader); err != nil {
				return err
			}

			if listPaths {
				fmt.Fprintln(out, friendlyPath(destPath)) //nolint:forbidigo // out is a generic io.Writer; production caller passes os.Stdout directly, not IOStreams
			}

			written += writtenPerFile
			if written >= zipReadLimit {
				return fmt.Errorf("extracted zip too large: limit is %d bytes", zipReadLimit)
			}
		}
	}
	return nil
}

func friendlyPath(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		return path
	}
	return rel
}

func DownloadArtifacts(apiClient *gitlab.Client, repo glrepo.Interface, path string, listPaths bool, refName string, jobName string) error {
	artifact, _, err := apiClient.Jobs.DownloadArtifactsFile(repo.FullName(), refName, &gitlab.DownloadArtifactsFileOptions{Job: &jobName}, nil)
	if err != nil {
		return err
	}

	return readZip(artifact, path, listPaths, defaultZIPReadLimit, defaultZIPFileLimit, os.Stdout)
}
