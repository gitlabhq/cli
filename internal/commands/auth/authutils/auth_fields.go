package authutils

import "gitlab.com/gitlab-org/cli/internal/config"

// oauth2FieldsToClear is the set of config keys describing an OAuth session.
// They only mean anything together: is_oauth2 without the refresh token leaves
// glab sending the stored token as a Bearer credential it cannot renew.
var oauth2FieldsToClear = []string{
	"is_oauth2",
	"oauth2_refresh_token",
	"oauth2_expiry_date",
}

// authFieldsToClear is the set of config keys that hold authentication credentials.
// Clearing these fields removes all stored credentials for a host regardless of
// which auth method was previously used. It does not include "use_keyring", which
// is a preference setting rather than a credential and must remain set so that
// ClearAuthFields can correctly delete keyring entries during cleanup.
var authFieldsToClear = append([]string{
	"token",
	"job_token",
}, oauth2FieldsToClear...)

// ClearAuthFields removes all authentication-related config entries for the
// given hostname. It does not touch non-auth settings such as git_protocol,
// api_host, or api_protocol.
func ClearAuthFields(cfg config.Config, hostname string) error {
	return clearFields(cfg, hostname, authFieldsToClear)
}

// ClearOAuth2Fields removes the OAuth session fields for the given hostname,
// leaving the stored token in place. Use it when a host keeps its credential
// but stops being OAuth-authenticated.
func ClearOAuth2Fields(cfg config.Config, hostname string) error {
	return clearFields(cfg, hostname, oauth2FieldsToClear)
}

func clearFields(cfg config.Config, hostname string, keys []string) error {
	for _, key := range keys {
		if err := cfg.Set(hostname, key, ""); err != nil {
			return err
		}
	}
	return nil
}
