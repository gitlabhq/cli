package api

import (
	"context"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

var _ gitlab.AuthSource = (*oauth2AccessTokenOnlyAuthSource)(nil)

type oauth2AccessTokenOnlyAuthSource struct {
	token string
}

func (as oauth2AccessTokenOnlyAuthSource) Init(context.Context, *gitlab.Client) error {
	return nil
}

func (as oauth2AccessTokenOnlyAuthSource) Header(_ context.Context) (string, string, error) {
	return gitlab.OAuthTokenHeaderName, "Bearer " + as.token, nil
}
