package auth

import (
	"fmt"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

func GetAuthenticatedClient(cfg config.Config, clientFunc func() (*gitlab.Client, error), io *iostreams.IOStreams) (*gitlab.Client, error) {
	instances, err := cfg.Hosts()
	if err != nil || len(instances) == 0 {
		return nil, fmt.Errorf("no GitLab instances have been authenticated with glab; run `%s` to authenticate", io.Color().Bold("glab auth login"))
	}

	labClient, err := clientFunc()
	if err != nil {
		return nil, fmt.Errorf("error using API client: %w", err)
	}

	return labClient, nil
}
