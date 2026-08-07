package get_token

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientauthenticationv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/agentutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	clientAuthenticationApiV1          = "client.authentication.k8s.io/v1"
	clientAuthenticationExecCredential = "ExecCredential"
	buffer                             = 5 * time.Minute
	flagTokenExpiryDuration            = "token-expiry-duration"
	flagCheckRevoked                   = "check-revoked"
	tokenExpiryDurationDefault         = 24 * time.Hour
	minTokenExpiryDuration             = 24 * time.Hour
)

var patScopes = []string{"k8s_proxy"}

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams

	agentID             int64
	tokenExpiryDuration time.Duration
	cacheMode           agentutils.CacheMode
	checkRevoked        bool
}

func NewCmdAgentGetToken(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
	}
	agentGetTokenCmd := &cobra.Command{
		Use:   "get-token [flags]",
		Short: `Create a personal access token for a GitLab Agent for Kubernetes.`,
		Long: heredoc.Docf(`
			The token has the %[1]sk8s_proxy%[1]s scope and is valid until the end of the current day.

			You might receive an email from your GitLab instance that a new personal
			access token has been created.
		`, "`"),
		Example: heredoc.Doc(`
			# Get a token for agent 123
			glab cluster agent get-token --agent 123`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}
	fl := agentGetTokenCmd.Flags()
	fl.Int64VarP(&opts.agentID, "agent", "a", 0, "The numerical Agent ID to connect to.")
	fl.DurationVar(&opts.tokenExpiryDuration, flagTokenExpiryDuration, tokenExpiryDurationDefault, "Duration for how long the generated tokens should be valid for. Minimum is 1 day and the effective expiry is always at the end of the day, the time is ignored.")
	fl.BoolVar(&opts.checkRevoked, flagCheckRevoked, false, "Check if a cached token is revoked. This requires an API call to GitLab which adds latency every time a cached token is accessed.")
	agentutils.AddTokenCacheModeFlag(fl, &opts.cacheMode)
	cobra.CheckErr(agentGetTokenCmd.MarkFlagRequired("agent"))

	cmdutils.AddJQFlag(agentGetTokenCmd, f.IO())
	return agentGetTokenCmd
}

func (o *options) validate() error {
	if o.tokenExpiryDuration < minTokenExpiryDuration {
		return fmt.Errorf("token expiry duration must be at least 24 hours")
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	pat, err := o.cachedPAT(ctx)
	if err != nil {
		return err
	}

	execCredential := clientauthenticationv1.ExecCredential{
		TypeMeta: metav1.TypeMeta{
			APIVersion: clientAuthenticationApiV1,
			Kind:       clientAuthenticationExecCredential,
		},
		Status: &clientauthenticationv1.ExecCredentialStatus{
			ExpirationTimestamp: &metav1.Time{Time: time.Time(*pat.ExpiresAt).Add(-buffer)},
			Token:               fmt.Sprintf("pat:%d:%s", o.agentID, pat.Token),
		},
	}

	return o.io.PrintJSON(execCredential)
}

func (o *options) cachedPAT(ctx context.Context) (*gitlab.PersonalAccessToken, error) {
	client, err := o.gitlabClient()
	if err != nil {
		return nil, err
	}

	createFunc := func() (*gitlab.PersonalAccessToken, error) {
		randomBytes := make([]byte, 16)

		_, err = rand.Read(randomBytes)
		if err != nil {
			return nil, err
		}

		patName := fmt.Sprintf("glab-k8s-proxy-%x", randomBytes)
		patExpiresAt := time.Now().Add(o.tokenExpiryDuration).UTC()

		pat, _, err := client.Users.CreatePersonalAccessTokenForCurrentUser(&gitlab.CreatePersonalAccessTokenForCurrentUserOptions{
			Name:      new(patName),
			Scopes:    new(patScopes),
			ExpiresAt: new(gitlab.ISOTime(patExpiresAt)),
		}, gitlab.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		return pat, nil
	}

	if o.cacheMode == agentutils.NoCacheCacheMode {
		return createFunc()
	}

	gitlabInstance := base64.StdEncoding.EncodeToString([]byte(client.BaseURL().String()))
	id := fmt.Sprintf("%s-%d", gitlabInstance, o.agentID)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pat, err := withLock(ctx, id, func() (*gitlab.PersonalAccessToken, error) {
		isTokenRevoked := func(t *gitlab.PersonalAccessToken) (bool, error) {
			if !o.checkRevoked {
				return false, nil
			}

			return isTokenRevoked(ctx, client, t)
		}
		switch o.cacheMode {
		case agentutils.ForcedKeyringCacheMode:
			return fromKeyringCache(id, createFunc, isTokenRevoked)
		case agentutils.ForcedFilesystemCacheMode:
			return fromFilesystemCache(id, createFunc, isTokenRevoked)
		case agentutils.KeyringFilesystemFallback:
			pat, err := fromKeyringCache(id, createFunc, isTokenRevoked)
			if err != nil {
				if !errors.Is(err, keyring.ErrUnsupportedPlatform) {
					return nil, err
				}
				return fromFilesystemCache(id, createFunc, isTokenRevoked)
			}
			return pat, nil
		default:
			panic(fmt.Sprintf("unimplemented cache mode: %s. This is a programming error, please report at https://gitlab.com/gitlab-org/cli/-/issues", o.cacheMode))
		}
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			o.io.LogErrorf("Unable to use cache, proceeding without it: %s\n", err)
			return createFunc()
		}

		return nil, err
	}

	return pat, nil
}

func fromKeyringCache(id string, createFunc func() (*gitlab.PersonalAccessToken, error), isTokenRevoked func(t *gitlab.PersonalAccessToken) (bool, error)) (*gitlab.PersonalAccessToken, error) {
	c := cache{
		storage:        &keyringStorage{},
		id:             id,
		createFunc:     createFunc,
		isTokenRevoked: isTokenRevoked,
	}

	return c.get()
}

func fromFilesystemCache(id string, createFunc func() (*gitlab.PersonalAccessToken, error), isTokenRevoked func(t *gitlab.PersonalAccessToken) (bool, error)) (*gitlab.PersonalAccessToken, error) {
	fs, err := newFileStorage()
	if err != nil {
		return nil, err
	}
	defer fs.close()

	c := cache{
		id:             id,
		createFunc:     createFunc,
		storage:        fs,
		isTokenRevoked: isTokenRevoked,
	}
	return c.get()
}

func isTokenRevoked(ctx context.Context, client *gitlab.Client, token *gitlab.PersonalAccessToken) (bool, error) {
	req, err := client.NewRequest(http.MethodGet, "personal_access_tokens/self", nil, []gitlab.RequestOptionFunc{
		gitlab.WithHeader(gitlab.AccessTokenHeaderName, token.Token),
		gitlab.WithContext(ctx),
	})
	if err != nil {
		return false, fmt.Errorf("unable to create request to check if token is revoked: %w", err)
	}

	var t gitlab.PersonalAccessToken
	_, err = client.Do(req, &t)
	if err == nil {
		return t.Revoked, nil
	}

	var errResp *gitlab.ErrorResponse
	if !errors.As(err, &errResp) {
		return false, nil
	}

	// check if token is revoked, the endpoint returns the following if it is (status code is 401):
	// {
	//   "error": "invalid_token",
	//   "error_description": "Token was revoked. You have to re-authorize from the user."
	// }

	if !errResp.HasStatusCode(http.StatusUnauthorized) {
		return false, err
	}
	var response errorResponse
	jsonErr := json.Unmarshal(errResp.Body, &response)
	if jsonErr != nil {
		return false, err // we want to return the original API error
	}

	return strings.HasPrefix(response.ErrorDescription, "Token was revoked."), nil
}

type errorResponse struct {
	ErrorDescription string `json:"error_description"`
}
