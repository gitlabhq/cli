package helpers

import (
	"fmt"
	"io"
	"os"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
)

func GetSecureFileByName(client *gitlab.Client, fileName, repoName string) (*gitlab.SecureFile, error) {
	options := &gitlab.ListProjectSecureFilesOptions{
		ListOptions: gitlab.ListOptions{
			Page:    1,
			PerPage: api.MaxPerPage,
		},
	}

	for secureFile, err := range gitlab.Scan2(func(p gitlab.PaginationOptionFunc) ([]*gitlab.SecureFile, *gitlab.Response, error) {
		return client.SecureFiles.ListProjectSecureFiles(repoName, options, p)
	}) {
		if err != nil {
			return nil, fmt.Errorf("error fetching secure files: %w", err)
		}

		if secureFile.Name == fileName {
			return secureFile, nil
		}
	}

	return nil, fmt.Errorf("couldn't locate secure file with name %s", fileName)
}

func GetReaderFromFilePath(filePath string) (io.Reader, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	return file, nil
}
