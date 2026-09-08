package add

import (
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func UploadDeployKey(client *gitlab.Client, projectId string, title string, key string, canPush bool, expiresAt string) error {
	deployKeyAddOptions := &gitlab.AddDeployKeyOptions{
		Title:   &title,
		Key:     &key,
		CanPush: &canPush,
	}

	if expiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return err
		}
		deployKeyAddOptions.ExpiresAt = &expiresAt
	}

	_, _, err := client.DeployKeys.AddDeployKey(projectId, deployKeyAddOptions)
	return err
}
