package artifactregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// maxRegistryResponseBytes bounds every body RegistryClient reads. A
// repository and its armored public keys are a few kilobytes.
const maxRegistryResponseBytes = 1 << 20

// ErrNoSigningKeyFingerprint is returned for a repository whose response
// carries no signing key fingerprint, so a caller has nothing to check a
// downloaded key against.
var ErrNoSigningKeyFingerprint = errors.New("the repository has no signing key fingerprint")

// StatusError is a non-2xx answer from the Artifact Registry. Code and Message
// come from the error envelope in the body, when it has one. A 403 from a
// namespace's condition gate carries its reason only there.
type StatusError struct {
	URL        string
	StatusCode int
	Code       string
	Message    string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("GET %s: %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
	if e.Code != "" {
		msg += fmt.Sprintf(" (%s)", e.Code)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// RegistryClient calls the Artifact Registry itself, not GitLab, with a token
// from the token exchange.
type RegistryClient struct {
	httpClient *http.Client
	token      string
}

// NewRegistryClient returns a RegistryClient that sends token on every
// request through httpClient. httpClient must carry no GitLab credential of
// its own. api.Client.HTTPClient also carries the GitLab host's proxy and TLS
// settings: the CA, the client certificate, and skip_tls_verify. A caller that
// trusts what it reads passes a client that verifies TLS.
func NewRegistryClient(httpClient *http.Client, token string) *RegistryClient {
	return &RegistryClient{httpClient: httpClient, token: token}
}

// SigningKeyFingerprints are the fingerprints of a repository's selected
// signing keys. Secondary is empty outside a key rotation. Format and Kind are
// the repository's, such as "rpm" and "hosted".
type SigningKeyFingerprints struct {
	Primary   string
	Secondary string
	Format    string
	Kind      string
}

// List returns the fingerprints that are set, primary first.
func (f SigningKeyFingerprints) List() []string {
	if f.Secondary == "" {
		return []string{f.Primary}
	}
	return []string{f.Primary, f.Secondary}
}

type repositoryResponse struct {
	Format   string `json:"format"`
	Kind     string `json:"kind"`
	Settings struct {
		SigningKeyFingerprint          string  `json:"signing_key_fingerprint"`
		SecondarySigningKeyFingerprint *string `json:"secondary_signing_key_fingerprint"`
	} `json:"settings"`
}

// SigningKeyFingerprints reads repository's selected signing key fingerprints
// with GET /api/v1/{slug}/repositories/{repository}, which needs
// read_repository. The registry masks a denied read, so a caller without it
// gets a 404, the same as for a missing repository. A 403 comes from a
// namespace condition, such as a blocked namespace, and its StatusError
// carries the reason. baseURL is the registry's scheme, host, and any path the
// deployment serves it under.
func (c *RegistryClient) SigningKeyFingerprints(ctx context.Context, baseURL *url.URL, slug, repository string) (*SigningKeyFingerprints, error) {
	u := baseURL.JoinPath("api", "v1", slug, "repositories", repository)
	body, err := c.get(ctx, u, func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+c.token) })
	if err != nil {
		return nil, err
	}

	var repo repositoryResponse
	if err := json.Unmarshal(body, &repo); err != nil {
		return nil, fmt.Errorf("decoding GET %s: %w", u.Redacted(), err)
	}
	if repo.Settings.SigningKeyFingerprint == "" {
		return nil, fmt.Errorf("GET %s: %w", u.Redacted(), ErrNoSigningKeyFingerprint)
	}

	fingerprints := &SigningKeyFingerprints{Primary: repo.Settings.SigningKeyFingerprint, Format: repo.Format, Kind: repo.Kind}
	if repo.Settings.SecondarySigningKeyFingerprint != nil {
		fingerprints.Secondary = *repo.Settings.SecondarySigningKeyFingerprint
	}
	return fingerprints, nil
}

// PublicKeys downloads the ASCII-armored public keys at repositoryURL's
// key.asc. A repository's client routes take HTTP Basic with the token as the
// password, so this uses the same credential as the packages.
func (c *RegistryClient) PublicKeys(ctx context.Context, repositoryURL *url.URL) ([]byte, error) {
	return c.get(ctx, repositoryURL.JoinPath("key.asc"), func(req *http.Request) { req.SetBasicAuth("token", c.token) })
}

func (c *RegistryClient) get(ctx context.Context, u *url.URL, authorize func(*http.Request)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	authorize(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryResponseBytes+1))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		statusErr := &StatusError{URL: u.Redacted(), StatusCode: resp.StatusCode}
		var envelope errorEnvelope
		if err == nil && len(body) <= maxRegistryResponseBytes && json.Unmarshal(body, &envelope) == nil {
			statusErr.Code = envelope.Error.Code
			statusErr.Message = envelope.Error.Message
		}
		return nil, statusErr
	}
	if err != nil {
		return nil, fmt.Errorf("reading GET %s: %w", u.Redacted(), err)
	}
	if len(body) > maxRegistryResponseBytes {
		return nil, fmt.Errorf("GET %s: the response is larger than %d bytes", u.Redacted(), maxRegistryResponseBytes)
	}
	return body, nil
}
