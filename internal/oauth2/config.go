package oauth2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	redirectURL              = "http://localhost:7171/auth/redirect"
	callbackServerListenAddr = ":7171"
)

var scopes = []string{"openid", "profile", "read_user", "write_repository", "api"}

const (
	clientIDChoicePaste  = "I have an Application ID to paste"
	clientIDChoiceShow   = "Show me what an admin needs to create"
	clientIDChoiceCancel = "Cancel"
)

func nonInteractiveClientIDErr(hostname string) error {
	return fmt.Errorf("set 'client_id' first with `glab config set client_id <client_id> -g --host %s`", hostname)
}

// oauthClientID looks up the OAuth client_id already configured for
// hostname, without prompting for one. A self-managed/Dedicated hostname
// with nothing configured yet resolves to ("", nil), leaving the decision
// of what to do about that to the caller.
func oauthClientID(cfg config.Config, hostname string) (string, error) {
	if !glinstance.IsSelfHosted(hostname) {
		return glinstance.DefaultClientID, nil
	}
	return cfg.Get(hostname, "client_id")
}

// resolveClientID resolves the OAuth client_id to use for hostname,
// prompting to register (or paste) one on a self-managed/Dedicated instance
// with nothing configured yet, if io is interactive. io must be non-nil:
// IsInteractive dereferences it unconditionally.
//
// The second return value persists clientID; the caller must invoke it only
// after actually completing an OAuth round trip with clientID, never before.
// A pasted value is unproven: a typo, an ID from another instance, or the
// application Secret pasted in place of the Application ID would otherwise
// be written immediately, permanently hiding this prompt behind a value that
// silently hangs the next login's OAuth callback wait instead of failing
// visibly. For an already-configured or GITLAB_CLIENT_ID-supplied value,
// persist is a no-op: there's nothing new to write.
func resolveClientID(ctx context.Context, cfg config.Config, io *iostreams.IOStreams, hostname string) (string, func() error, error) {
	clientID, err := oauthClientID(cfg, hostname)
	if err != nil {
		return "", nil, err
	}
	if clientID != "" {
		return clientID, func() error { return nil }, nil
	}

	if !io.IsInteractive() {
		return "", nil, nonInteractiveClientIDErr(hostname)
	}

	return promptClientIDChoice(ctx, cfg, io, hostname)
}

// promptClientIDChoice asks how to proceed once a self-managed hostname
// turns out to have no client_id configured locally. An absent config value
// says nothing about whether an admin already registered an application on
// the instance, so the missing-value case and the need-to-create-one case
// get their own paths instead of one prompt that assumes the latter.
func promptClientIDChoice(ctx context.Context, cfg config.Config, io *iostreams.IOStreams, hostname string) (string, func() error, error) {
	var choice string
	title := fmt.Sprintf("No OAuth application ID is configured for %s.\n\nHow do you want to continue?", hostname)
	if err := io.Select(ctx, &choice, title, []string{clientIDChoicePaste, clientIDChoiceShow, clientIDChoiceCancel}); err != nil {
		if errors.Is(err, iostreams.ErrUserCancelled) {
			return "", nil, nonInteractiveClientIDErr(hostname)
		}
		return "", nil, err
	}

	switch choice {
	case clientIDChoicePaste:
		return promptClientID(ctx, cfg, io, hostname)
	case clientIDChoiceShow:
		printClientIDRegistrationInstructions(io)
		return "", nil, nonInteractiveClientIDErr(hostname)
	default: // clientIDChoiceCancel
		return "", nil, nonInteractiveClientIDErr(hostname)
	}
}

// printClientIDRegistrationInstructions prints the values an instance or
// group admin needs to register an OAuth application for glab. It never
// persists anything: the caller still returns nonInteractiveClientIDErr
// afterwards, so the exact `config set` command to run once an ID exists has
// one source of truth regardless of which exit path got here.
func printClientIDRegistrationInstructions(io *iostreams.IOStreams) {
	io.LogError()
	io.LogError(heredoc.Docf(`
		Ask an instance or group admin to create one (Admin Area > Applications,
		or Group > Settings > Applications):
		  Redirect URI: %[1]s
		  Scopes:       %[2]s
		  Confidential: off (glab is a public client and cannot hold a secret)

		See also: https://docs.gitlab.com/cli/authentication/#oauth-gitlab-self-managed-gitlab-dedicated
	`, redirectURL, strings.Join(scopes, " ")))
}

// promptClientID asks for the Application ID to paste. It does not persist
// it: the returned func does, exactly the way
// `glab config set client_id <id> -g --host <hostname>` would, once the
// caller has proven the value by completing an OAuth round trip with it.
func promptClientID(ctx context.Context, cfg config.Config, io *iostreams.IOStreams, hostname string) (string, func() error, error) {
	var clientID string
	err := io.Input(ctx, &clientID, "Paste the Application ID here:", "", func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("required")
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, iostreams.ErrUserCancelled) {
			return "", nil, nonInteractiveClientIDErr(hostname)
		}
		return "", nil, err
	}
	clientID = strings.TrimSpace(clientID)

	persist := func() error {
		if err := cfg.Set(hostname, "client_id", clientID); err != nil {
			return fmt.Errorf("failed to set 'client_id': %w", err)
		}
		if err := cfg.Write(); err != nil {
			return fmt.Errorf("failed to write configuration to disk: %w", err)
		}
		return nil
	}

	return clientID, persist, nil
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
