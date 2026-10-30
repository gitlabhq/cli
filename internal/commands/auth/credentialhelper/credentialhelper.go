package credentialhelper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	errorCodeNotAuthenticated      = "not_authenticated"
	errorCodeUnsupportedAuthSource = "unsupported_auth_source"
	errorCodeRefreshFailed         = "refresh_failed"
	errorCodeKeyringUnavailable    = "keyring_unavailable"
	errorCodeInvalidRepoOverride   = "invalid_repo_override"
	errorCodeUnknown               = "unknown"
)

type responseType any

type errorResponseType struct{}

func (errorResponseType) MarshalJSON() ([]byte, error) {
	return []byte(`"error"`), nil
}

type successResponseType struct{}

func (successResponseType) MarshalJSON() ([]byte, error) {
	return []byte(`"success"`), nil
}

type response struct {
	Type        successResponseType `json:"type"` // always evaluates to "success"
	InstanceURL string              `json:"instance_url"`
	Token       token               `json:"token"`
}

type token struct {
	Type            string    `json:"type"`
	Token           string    `json:"token"`
	ExpiryTimestamp time.Time `json:"expiry_timestamp,omitzero"`
}

type errorResponse struct {
	Type       errorResponseType `json:"type"` // always evaluates to "error"
	Code       string            `json:"code"`
	Host       string            `json:"host,omitempty"`
	OAuthError string            `json:"oauth_error,omitempty"`
	Message    string            `json:"message"`
}

type options struct {
	baseRepo        func() (glrepo.Interface, error)
	apiClient       func(repoHost string) (*api.Client, error)
	defaultHostname func() string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		baseRepo:        f.BaseRepo,
		apiClient:       f.ApiClient,
		defaultHostname: f.DefaultHostname,
	}

	writeResponse := func(resp responseType) error {
		io := f.IO()
		enc := json.NewEncoder(io.StdOut) //nolint:forbidigo // git credential helper protocol writes JSON to both stdout and stderr; PrintJSON only handles stdout
		if err := enc.Encode(resp); err != nil {
			errEnc := json.NewEncoder(io.StdErr) //nolint:forbidigo // git credential helper protocol error response on stderr
			if err := errEnc.Encode(errorResponse{Code: errorCodeUnknown, Message: err.Error()}); err != nil {
				return err
			}
			return cmdutils.SilentError
		}
		return nil
	}

	cmd := &cobra.Command{
		Use:               "credential-helper [flags]",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Short:             "Implements a generic credential helper.",
		Long: heredoc.Docf(`
			Outputs a JSON object on stdout with %[1]stype%[1]s set to %[1]ssuccess%[1]s or %[1]serror%[1]s.

			An %[1]serror%[1]s response always has a %[1]scode%[1]s field. Use it instead of matching on the %[1]smessage%[1]s text, which can change. The codes are:

			- %[1]snot_authenticated%[1]s: glab has no credentials for the host.
			- %[1]sunsupported_auth_source%[1]s: the authentication method in use does not provide a token.
			- %[1]srefresh_failed%[1]s: the OAuth2 token could not be refreshed.
			- %[1]skeyring_unavailable%[1]s: the operating system keyring could not be read or written.
			- %[1]sinvalid_repo_override%[1]s: the %[1]s--repo%[1]s flag or the %[1]sGITLAB_REPO%[1]s environment variable is not valid.
			- %[1]sunknown%[1]s: any other error.

			An %[1]serror%[1]s response also has %[1]shost%[1]s whenever glab resolved a host, and %[1]soauth_error%[1]s when the OAuth2 server returned an error code such as %[1]sinvalid_grant%[1]s.

			Callers must parse the JSON response on stdout to detect %[1]stype: "error"%[1]s responses rather than relying on the process exit code, because credential resolution errors exit with status 0 while flag validation errors exit with a non-zero status.
		`, "`"),
		Hidden: true,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			resp := opts.run(cmd.Context())

			return writeResponse(resp)
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	// NOTE: this is a hack to ensure the JSON protocol for the hook that EnableRepoOverride added.
	repoOverridePersistentPreRunE := cmd.PersistentPreRunE
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		err := repoOverridePersistentPreRunE(cmd, args)
		if err != nil {
			_ = writeResponse(errorResponse{
				Code:    errorCodeInvalidRepoOverride,
				Message: err.Error(),
			})
			// We need to signal cobra that we want to error, but that cobra shouldn't log anything else.
			// This silent error is the way to go here.
			return cmdutils.SilentError
		}
		return nil
	}

	return cmd
}

func (o *options) run(ctx context.Context) responseType {
	host := o.defaultHostname()
	if baseRepo, err := o.baseRepo(); err == nil {
		host = baseRepo.RepoHost()
	}

	apiClient, err := o.apiClient(host)
	if err != nil {
		return errorResponse{
			Code:    errorCode(err),
			Host:    host,
			Message: err.Error(),
		}
	}

	// NOTE: the API client ensures this suffix via glinstance.APIEndpoint().
	instanceURL := strings.TrimSuffix(apiClient.BaseURL(), "/api/v4/")

	cred, err := apiClient.Credential(ctx)
	switch {
	case errors.Is(err, api.ErrUnauthenticated):
		return errorResponse{
			Code:    errorCodeNotAuthenticated,
			Host:    host,
			Message: "glab is not authenticated. Use glab auth login to authenticate",
		}
	case errors.Is(err, api.ErrUnsupportedAuthSource):
		return errorResponse{
			Code:    errorCodeUnsupportedAuthSource,
			Host:    host,
			Message: "unable to determine token",
		}
	case err != nil:
		var oauthError string
		if retrieveErr, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
			oauthError = retrieveErr.ErrorCode
		}
		return errorResponse{
			Code:       errorCode(err),
			Host:       host,
			OAuthError: oauthError,
			Message:    fmt.Sprintf("failed to read the credentials for %q: %v", host, err),
		}
	}

	return response{
		InstanceURL: instanceURL,
		Token: token{
			Type:            string(cred.Kind),
			Token:           cred.Token,
			ExpiryTimestamp: cred.Expiry.UTC(),
		},
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, config.ErrKeyringUnavailable):
		return errorCodeKeyringUnavailable
	case errors.Is(err, api.ErrUnauthenticated):
		return errorCodeNotAuthenticated
	case errors.Is(err, api.ErrUnsupportedAuthSource):
		return errorCodeUnsupportedAuthSource
	case errors.Is(err, api.ErrRefreshFailed):
		return errorCodeRefreshFailed
	default:
		return errorCodeUnknown
	}
}
