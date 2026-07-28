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

func unmarshal(hostname string, cfg config.Config) (*oauth2.Token, error) {
	result := &oauth2.Token{}
	var err error

	expiryDateString, err := cfg.Get(hostname, "oauth2_expiry_date")
	if err != nil {
		return nil, err
	}

	result.Expiry, err = time.Parse(time.RFC3339, expiryDateString)
	if err != nil {
		// Fall back to RFC822/RFC822Z for configs written by older versions of glab.
		result.Expiry, err = time.Parse(time.RFC822, expiryDateString)
		if err != nil {
			result.Expiry, err = time.Parse(time.RFC822Z, expiryDateString)
			if err != nil {
				return nil, err
			}
		}
	}

	result.RefreshToken, err = cfg.Get(hostname, "oauth2_refresh_token")
	if err != nil {
		return nil, err
	}

	result.AccessToken, err = cfg.Get(hostname, "token")
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
