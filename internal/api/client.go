package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/oauth2"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// ClientOption represents a function that configures a Client
type ClientOption func(*Client) error

type BuildInfo struct {
	Version, Commit, Platform, Architecture string
	CodingAgent                             string
}

func (i BuildInfo) UserAgent() string {
	ua := fmt.Sprintf("glab/%s (%s, %s)", i.Version, i.Platform, i.Architecture)
	if i.CodingAgent != "" {
		ua += " Coding-Agent/" + i.CodingAgent
	}
	return ua
}

// Client represents an argument to NewClient
type Client struct {
	// gitlabClient represents GitLab API client.
	gitlabClient *gitlab.Client
	// internal http client
	httpClient *http.Client
	// custom certificate
	caFile string
	// client certificate files
	clientCertFile string
	clientKeyFile  string

	baseURL    string
	authSource gitlab.AuthSource

	allowInsecure bool

	userAgent string

	customHeaders          map[string]string
	customHeaderExtraHosts map[string]struct{}
	proxy                  func(*http.Request) (*url.URL, error)
}

func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// AuthSource returns the auth source
// TODO: clarify use cases for this.
func (c *Client) AuthSource() gitlab.AuthSource {
	return c.authSource
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

// Lab returns the initialized GitLab client.
func (c *Client) Lab() *gitlab.Client {
	return c.gitlabClient
}

var secureCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
}

type newAuthSource func(c *http.Client) (authSource gitlab.AuthSource, err error)

// NewClient initializes a api client for use throughout glab.
func NewClient(newAuthSource newAuthSource, options ...ClientOption) (*Client, error) {
	// 0. initialize empty Client
	client := &Client{
		proxy: http.ProxyFromEnvironment,
	}

	// 1. apply provided option functions to populate client
	for _, option := range options {
		if err := option(client); err != nil {
			return nil, fmt.Errorf("failed to apply client option: %w", err)
		}
	}

	// 2. initialize HTTP client used by the auth source and by the GitLab client
	if err := client.initializeHTTPClient(); err != nil {
		return nil, err
	}

	// 3. initialize the auth source
	// We need to delay this because sources like OAuth2 need a valid
	// HTTP client to refresh the token.
	authSource, err := newAuthSource(client.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth source: %w", err)
	}
	client.authSource = authSource

	// 4. initialize the GitLab client
	if client.gitlabClient != nil {
		return client, nil
	}

	if client.authSource == nil {
		return nil, errors.New("unable to initialize GitLab Client because no authentication source is provided. Login first")
	}

	gitlabClient, err := gitlab.NewAuthSourceClient(
		client.authSource,
		gitlab.WithHTTPClient(client.httpClient),
		gitlab.WithBaseURL(client.baseURL),
		gitlab.WithUserAgent(client.userAgent),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GitLab client: %w", err)
	}

	client.gitlabClient = gitlabClient
	return client, nil
}

func (c *Client) initializeHTTPClient() error {
	if c.httpClient != nil {
		return nil
	}

	// Create TLS configuration based on client settings
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: c.allowInsecure,
	}

	// Set secure cipher suites for gitlab.com
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	if !glinstance.IsSelfHosted(u.Hostname()) {
		tlsConfig.CipherSuites = secureCipherSuites
	}

	// Configure custom CA if provided
	if c.caFile != "" {
		caCert, err := os.ReadFile(c.caFile)
		if err != nil {
			return fmt.Errorf("error reading cert file: %w", err)
		}
		// use system cert pool as a baseline
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return err
		}
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	// Configure client certificates if provided
	if c.clientCertFile != "" && c.clientKeyFile != "" {
		clientCert, err := tls.LoadX509KeyPair(c.clientCertFile, c.clientKeyFile)
		if err != nil {
			return err
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	// Set appropriate timeouts based on whether custom CA is used
	dialTimeout := 5 * time.Second
	keepAlive := 5 * time.Second
	idleTimeout := 30 * time.Second
	if c.caFile != "" {
		dialTimeout = 30 * time.Second
		keepAlive = 30 * time.Second
		idleTimeout = 90 * time.Second
	}

	var rt http.RoundTripper = &http.Transport{
		Proxy: c.proxy,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}

	if enabled, found := utils.IsEnvVarEnabled("GLAB_DEBUG_HTTP"); found && enabled {
		rt = &debugTransport{rt: rt, w: os.Stderr}
	}

	if len(c.customHeaders) > 0 {
		allowedHosts := make(map[string]struct{}, len(c.customHeaderExtraHosts)+1)
		for host := range c.customHeaderExtraHosts {
			allowedHosts[strings.ToLower(host)] = struct{}{}
		}
		allowedHosts[strings.ToLower(u.Host)] = struct{}{}
		rt = &customHeadersTransport{rt: rt, headers: c.customHeaders, allowedHosts: allowedHosts}
	}

	c.httpClient = &http.Client{Transport: rt}
	return nil
}

// WithProxy allows overriding the proxy function for the transport
func WithProxy(proxy func(*http.Request) (*url.URL, error)) ClientOption {
	return func(c *Client) error {
		c.proxy = proxy
		return nil
	}
}

// WithCustomHeaders sets custom headers for requests to the client's base URL.
// extraHosts covers destinations the base URL does not, notably the OAuth refresh
// endpoint, which uses the configured host even when api_host points elsewhere.
func WithCustomHeaders(headers map[string]string, extraHosts ...string) ClientOption {
	return func(c *Client) error {
		c.customHeaders = headers
		c.customHeaderExtraHosts = make(map[string]struct{}, len(extraHosts))
		for _, host := range extraHosts {
			c.customHeaderExtraHosts[strings.ToLower(host)] = struct{}{}
		}
		return nil
	}
}

// WithCustomCA configures the client to use a custom CA certificate
func WithCustomCA(caFile string) ClientOption {
	return func(c *Client) error {
		c.caFile = caFile
		return nil
	}
}

// WithClientCertificate configures the client to use client certificates for mTLS
func WithClientCertificate(certFile, keyFile string) ClientOption {
	return func(c *Client) error {
		c.clientCertFile = certFile
		c.clientKeyFile = keyFile
		return nil
	}
}

// WithInsecureSkipVerify configures the client to skip TLS verification
func WithInsecureSkipVerify(skip bool) ClientOption {
	return func(c *Client) error {
		c.allowInsecure = skip
		return nil
	}
}

// WithHTTPClient configures the HTTP client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) error {
		c.httpClient = httpClient
		return nil
	}
}

// WithGitLabClient configures the GitLab client
func WithGitLabClient(client *gitlab.Client) ClientOption {
	return func(c *Client) error {
		c.gitlabClient = client
		return nil
	}
}

// WithBaseURL configures the base URL for the GitLab instance
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) error {
		c.baseURL = baseURL
		return nil
	}
}

// WithUserAgent configures the user agent to use
func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) error {
		c.userAgent = userAgent
		return nil
	}
}

// FromConfigOption customizes how NewClientFromConfig resolves credentials
// out of config. Distinct from ClientOption, which configures the client
// that has already been built: this affects resolution that happens before
// a Client exists.
type FromConfigOption func(*fromConfigOptions)

type fromConfigOptions struct {
	// searchEnvForIdentity controls whether the access token and the
	// is_oauth2 flag may come from the environment (GITLAB_TOKEN,
	// GITLAB_ACCESS_TOKEN, GLAB_IS_OAUTH2) in addition to config. job_token
	// is deliberately excluded from this option: CI_JOB_TOKEN authentication
	// must keep working under CI auto-login regardless.
	searchEnvForIdentity bool
}

// WithoutTokenFromEnvironment resolves the access token and the is_oauth2
// flag from config only, ignoring GITLAB_TOKEN, GITLAB_ACCESS_TOKEN, and
// GLAB_IS_OAUTH2.
//
// Use it wherever glab acts on behalf of another process that inherits the
// user's shell environment, such as a Docker credential helper, so a stray
// environment variable cannot decide which identity the request is made as
// or which auth flow (OAuth2 vs. PAT) it uses. job_token is unaffected, so
// CI_JOB_TOKEN authentication keeps working under CI auto-login.
func WithoutTokenFromEnvironment() FromConfigOption {
	return func(o *fromConfigOptions) {
		o.searchEnvForIdentity = false
	}
}

// NewClientFromConfig initializes the global api with the config data
func NewClientFromConfig(repoHost string, cfg config.Config, isGraphQL bool, userAgent string, opts ...FromConfigOption) (*Client, error) {
	fromCfg := fromConfigOptions{searchEnvForIdentity: true}
	for _, opt := range opts {
		opt(&fromCfg)
	}

	apiHost, _ := cfg.Get(repoHost, "api_host")
	if apiHost == "" {
		apiHost = repoHost
	}
	subfolder, _ := cfg.Get(repoHost, "subfolder")

	apiProtocol, _ := cfg.Get(repoHost, "api_protocol")
	if apiProtocol == "" {
		apiProtocol = glinstance.DefaultProtocol
	}

	isOAuth2Cfg, isOAuth2Source, _ := cfg.GetWithSource(repoHost, "is_oauth2", fromCfg.searchEnvForIdentity)

	// token and job_token may be backed by the OS keyring. Surface read errors
	// (locked keyring, denied access, unavailable backend) instead of silently
	// treating them as an empty credential, which produces confusing downstream
	// auth errors. A credential that is simply not stored returns "" with no
	// error.
	token, tokenSource, err := cfg.GetWithSource(repoHost, "token", fromCfg.searchEnvForIdentity)
	if err != nil {
		return nil, fmt.Errorf("failed to read the access token for %q: %w", repoHost, err)
	}

	isOAuth2 := isOAuth2Cfg == "true"
	isEnvironmentPAT := tokenSource == "GITLAB_TOKEN" || tokenSource == "GITLAB_ACCESS_TOKEN"
	isEnvironmentOAuth2 := isOAuth2Source == "GLAB_IS_OAUTH2"
	if isEnvironmentPAT && !isEnvironmentOAuth2 {
		isOAuth2 = false
	}
	jobToken, err := cfg.Get(repoHost, "job_token")
	if err != nil {
		return nil, fmt.Errorf("failed to read the job token for %q: %w", repoHost, err)
	}
	tlsVerify, _ := cfg.Get(repoHost, "skip_tls_verify")
	skipTlsVerify := tlsVerify == "true" || tlsVerify == "1"
	caCert, _ := cfg.Get(repoHost, "ca_cert")
	clientCert, _ := cfg.Get(repoHost, "client_cert")
	keyFile, _ := cfg.Get(repoHost, "client_key")
	proxy, err := ProxyFromConfig(cfg, repoHost)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve proxy function: %w", err)
	}

	// Build options based on configuration
	options := []ClientOption{
		WithUserAgent(userAgent),
		WithProxy(proxy),
	}

	// Resolve custom headers from config
	headers, err := config.ResolveCustomHeaders(cfg, repoHost)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve custom headers: %w", err)
	}

	// DUO_WORKFLOW_WORKFLOW_ID is an internal contract intentionally not in user-facing
	// env-var docs. Set by GitLab's Workflow service so the API server can correlate
	// CLI traffic back to the originating workflow. Takes precedence over the same
	// header set via config. Read directly rather than via EnvKeyEquivalence because
	// the value is per-process with no config-file equivalent. Skip the header entirely
	// on malformed input (CR/LF/NUL).
	if duoWorkflowID := os.Getenv("DUO_WORKFLOW_WORKFLOW_ID"); duoWorkflowID != "" && !strings.ContainsAny(duoWorkflowID, "\r\n\x00") {
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["X-Gitlab-Duo-Workflow-Id"] = duoWorkflowID
	}

	if len(headers) > 0 {
		// OAuth refresh uses repoHost even when API requests use a separately
		// configured api_host, so allow both destinations.
		options = append(options, WithCustomHeaders(headers, repoHost))
	}

	// determine auth source
	var newAuthSource newAuthSource
	switch {
	case isOAuth2:
		// If the refresh token can't be read but a usable access token is still
		// present, fall through to access-token-only auth rather than failing
		// the whole client build. Log the read failure (surfaced with DEBUG) so
		// it is not lost; a hard error still occurs at refresh time when renewal
		// is actually attempted.
		refreshToken, refreshErr := cfg.Get(repoHost, "oauth2_refresh_token")
		if refreshErr != nil {
			if token == "" {
				return nil, fmt.Errorf("failed to read the OAuth2 refresh token for %q: %w", repoHost, refreshErr)
			}
			dbg.Debugf("failed to read oauth2_refresh_token for %q: %v", repoHost, refreshErr)
		}
		if refreshToken == "" {
			if token == "" {
				return nil, fmt.Errorf("OAuth2 authentication is configured for %q but no access or refresh token was found (the stored credentials may be missing or incomplete); run `glab auth login --hostname %s` to re-authenticate", repoHost, repoHost)
			}

			newAuthSource = func(client *http.Client) (gitlab.AuthSource, error) {
				return oauth2AccessTokenOnlyAuthSource{token: token}, nil
			}
			break
		}

		newAuthSource = func(client *http.Client) (gitlab.AuthSource, error) {
			ts, err := oauth2.NewConfigTokenSource(cfg, client, glinstance.DefaultProtocol, repoHost, fromCfg.searchEnvForIdentity)
			if err != nil {
				return nil, err
			}
			return gitlab.OAuthTokenSource{TokenSource: ts}, nil
		}
	case token != "":
		// Check for PAT first since it's more common than job tokens
		newAuthSource = func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: token}, nil
		}
	case jobToken != "":
		newAuthSource = func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.JobTokenAuthSource{Token: jobToken}, nil
		}
	default:
		// NOTE: use an unauthenticated client.
		newAuthSource = func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.Unauthenticated{}, nil
		}
	}

	var baseURL string
	if isGraphQL {
		baseURL = glinstance.GraphQLEndpoint(repoHost, apiProtocol, apiHost, subfolder)
	} else {
		baseURL = glinstance.APIEndpoint(repoHost, apiProtocol, apiHost, subfolder)
	}
	options = append(options, WithBaseURL(baseURL))

	if caCert != "" {
		options = append(options, WithCustomCA(caCert))
	}

	if clientCert != "" && keyFile != "" {
		options = append(options, WithClientCertificate(clientCert, keyFile))
	}

	if skipTlsVerify {
		options = append(options, WithInsecureSkipVerify(skipTlsVerify))
	}

	return NewClient(newAuthSource, options...)
}

func NewHTTPRequest(ctx context.Context, c *Client, method string, baseURL *url.URL, body io.Reader, headers []string, bodyIsJSON bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, baseURL.String(), body)
	if err != nil {
		return nil, err
	}

	// Add any headers passed directly to this function
	for _, h := range headers {
		idx := strings.IndexRune(h, ':')
		if idx == -1 {
			return nil, fmt.Errorf("header %q requires a value separated by ':'", h)
		}
		name, value := h[0:idx], strings.TrimSpace(h[idx+1:])
		if strings.EqualFold(name, "Content-Length") {
			length, err := strconv.ParseInt(value, 10, 0)
			if err != nil {
				return nil, err
			}
			req.ContentLength = length
		} else {
			req.Header.Add(name, value)
		}
	}

	if bodyIsJSON && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	if c.Lab().UserAgent != "" {
		req.Header.Set("User-Agent", c.Lab().UserAgent)
	}

	// gitlab.Unauthenticated's Header returns a sentinel error; the go-gitlab
	// client's AllHeadersAuthStrategy skips the header in that case. Mirror
	// that here so `glab api` works on public endpoints without auth.
	if _, ok := c.authSource.(gitlab.Unauthenticated); !ok {
		name, value, err := c.authSource.Header(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set(name, value)
	}

	return req, nil
}

// Is404 checks if the error represents a 404 response
func Is404(err error) bool {
	// If the error is a typed response
	errResponse := &gitlab.ErrorResponse{}
	if errors.As(err, &errResponse) &&
		errResponse.Response != nil &&
		errResponse.Response.StatusCode == http.StatusNotFound {
		return true
	}

	// This can also come back as a string 404 from gitlab client-go
	if err != nil && err.Error() == "404 Not Found" {
		return true
	}

	return false
}
