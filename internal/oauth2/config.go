package oauth2

import (
	"fmt"
	"time"

	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

const (
	redirectURL              = "http://localhost:7171/auth/redirect"
	callbackServerListenAddr = ":7171"
)

var scopes = []string{"openid", "profile", "read_user", "write_repository", "api"}

func oauthClientID(cfg config.Config, hostname string) (string, error) {
	if glinstance.IsSelfHosted(hostname) {
		clientID, err := cfg.Get(hostname, "client_id")
		if err != nil {
			return "", err
		}

		if clientID == "" {
			return "", fmt.Errorf("set 'client_id' first with `glab config set client_id <client_id> -g --host %s`", hostname)
		}
		return clientID, nil
	}
	return glinstance.DefaultClientID, nil
}

// unmarshal reads the OAuth2 token for hostname out of cfg. The "token" key
// is also resolvable from GITLAB_TOKEN/GITLAB_ACCESS_TOKEN/OAUTH_TOKEN, so
// searchEnvForIdentity gates that lookup the same way it does in
// api.NewClientFromConfig: callers that act on behalf of another process
// inheriting the environment (the Docker credential helper) must pass false
// so a stray environment variable cannot substitute a different identity's
// access token here.
// ParseExpiryDate reads a stored oauth2_expiry_date, accepting the RFC822 forms
// older versions of glab wrote.
func ParseExpiryDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, time.RFC822, time.RFC822Z} {
		if expiry, err := time.Parse(layout, s); err == nil {
			return expiry, nil
		}
	}

	return time.Time{}, fmt.Errorf("could not parse %q as an expiry date", s)
}

func unmarshal(hostname string, cfg config.Config, searchEnvForIdentity bool) (*oauth2.Token, error) {
	result := &oauth2.Token{}
	var err error

	expiryDateString, err := cfg.Get(hostname, "oauth2_expiry_date")
	if err != nil {
		return nil, err
	}

	result.Expiry, err = ParseExpiryDate(expiryDateString)
	if err != nil {
		return nil, err
	}

	result.RefreshToken, err = cfg.Get(hostname, "oauth2_refresh_token")
	if err != nil {
		return nil, err
	}

	result.AccessToken, _, err = cfg.GetWithSource(hostname, "token", searchEnvForIdentity)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func marshal(hostname string, cfg config.Config, token *oauth2.Token) error {
	err := cfg.Set(hostname, "is_oauth2", "true")
	if err != nil {
		return err
	}

	if token.RefreshToken != "" {
		err = cfg.Set(hostname, "oauth2_refresh_token", token.RefreshToken)
		if err != nil {
			return err
		}
	}

	err = cfg.Set(hostname, "oauth2_expiry_date", token.Expiry.Format(time.RFC3339))
	if err != nil {
		return err
	}

	err = cfg.Set(hostname, "token", token.AccessToken)
	if err != nil {
		return err
	}

	return nil
}
