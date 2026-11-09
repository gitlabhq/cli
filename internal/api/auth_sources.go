package api

import (
	"context"
	"fmt"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

var _ gitlab.AuthSource = (*oauth2AccessTokenOnlyAuthSource)(nil)

type oauth2AccessTokenOnlyAuthSource struct {
	token string
	host  string

	// expiry is what the host last recorded, zero when nothing was stored.
	expiry time.Time
}

func (as oauth2AccessTokenOnlyAuthSource) Init(context.Context, *gitlab.Client) error {
	return nil
}

// checkExpiry refuses a token known to have expired: GitLab would only answer
// 401, and nothing here can renew it. Header and Credential both go through it
// so no caller gets a dead token back.
func (as oauth2AccessTokenOnlyAuthSource) checkExpiry() error {
	if !as.expiry.IsZero() && time.Now().After(as.expiry) {
		return fmt.Errorf("the OAuth2 session for %q has expired and cannot be renewed; run `glab auth login --hostname %s` to re-authenticate", as.host, as.host)
	}
	return nil
}

func (as oauth2AccessTokenOnlyAuthSource) Header(_ context.Context) (string, string, error) {
	if err := as.checkExpiry(); err != nil {
		return "", "", err
	}
	return gitlab.OAuthTokenHeaderName, "Bearer " + as.token, nil
}
