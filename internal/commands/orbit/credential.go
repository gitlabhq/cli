package orbit

import (
	"context"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

func orbitCredentialEnv(ctx context.Context, f cmdutils.Factory) []string {
	hostname := f.DefaultHostname()

	client, err := f.ApiClient(hostname)
	if err != nil {
		dbg.Debugf("orbit: no API client: %v", err)
		return nil
	}

	name, value, err := client.AuthSource().Header(ctx)
	if err != nil || name == "" || value == "" {
		dbg.Debugf("orbit: no auth header: %v", err)
		return nil
	}

	baseURL := strings.TrimSuffix(strings.TrimSuffix(client.BaseURL(), "/"), "/api/v4")
	return []string{
		"ORBIT_API_BASE_URL=" + baseURL,
		"ORBIT_AUTH_HEADER_NAME=" + name,
		"ORBIT_AUTH_HEADER_VALUE=" + value,
	}
}
