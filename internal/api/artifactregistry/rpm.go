package artifactregistry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	// rpmHandleRe is the registry handle: lowercase letters, digits and
	// hyphens. dnf variable names are built from it, so it is checked rather
	// than escaped.
	rpmHandleRe = regexp.MustCompile(`^[a-z0-9-]{1,255}$`)
	// rpmRepositoryNameRe is RepositoryName in the registry's OpenAPI.
	rpmRepositoryNameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,253}[a-z0-9])?$`)
	fingerprintRe       = regexp.MustCompile(`^[0-9A-F]{40}$`)
	// rpmHostRe leaves out IPv6 literals: their brackets and colons have no
	// spelling in a dnf variable name.
	rpmHostRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// RPMRepository is a hosted RPM repository, addressed the way dnf's baseurl
// addresses it: https://host[:port]/<registry handle>/rpm/<name>. Build one
// with ParseRPMRepository, which gives each repository a single spelling.
type RPMRepository struct {
	host   string
	handle string
	name   string
}

// Host is the repository's host, lowercased, with a port only when it is not
// 443.
func (r *RPMRepository) Host() string { return r.host }

// Handle is the registry handle.
func (r *RPMRepository) Handle() string { return r.handle }

// Name is the repository name.
func (r *RPMRepository) Name() string { return r.name }

// BaseURL is the repository URL, with no trailing slash.
func (r *RPMRepository) BaseURL() *url.URL {
	return &url.URL{Scheme: "https", Host: r.host, Path: "/" + r.handle + "/rpm/" + r.name}
}

// ParseRPMRepository parses raw as an RPM repository URL. It refuses anything
// that would need escaping in a dnf .repo file or a dnf variable name.
func ParseRPMRepository(raw string) (*RPMRepository, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("the scheme must be https (got %q)", u.Scheme)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, errors.New("must be https://host/<registry handle>/rpm/<repository>, with no user info, query or fragment")
	}
	hostname := strings.ToLower(u.Hostname())
	if !rpmHostRe.MatchString(hostname) {
		return nil, fmt.Errorf("the host must contain only letters, digits, '.', '_' and '-' (got %q); an internationalized host must be given in punycode", u.Hostname())
	}
	if strings.HasSuffix(hostname, ".") {
		return nil, fmt.Errorf("the host must not end with '.' (got %q)", u.Hostname())
	}
	host, err := canonicalHost(hostname, u.Port())
	if err != nil {
		return nil, err
	}
	segments := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.EscapedPath(), "/"), "/"), "/")
	if len(segments) != 3 || segments[1] != "rpm" {
		return nil, fmt.Errorf("the path must be /<registry handle>/rpm/<repository> (got %q)", u.Path)
	}
	handle, name := segments[0], segments[2]
	if !rpmHandleRe.MatchString(handle) {
		return nil, fmt.Errorf("the registry handle must contain only lowercase letters, digits and '-' (got %q)", handle)
	}
	if !rpmRepositoryNameRe.MatchString(name) {
		return nil, fmt.Errorf("the repository name must contain only lowercase letters, digits, '.', '_' and '-', and start and end with a letter or digit (got %q)", name)
	}
	return &RPMRepository{host: host, handle: handle, name: name}, nil
}

// canonicalHost drops an empty or default port, so https://host,
// https://host: and https://host:443 are one repository.
func canonicalHost(hostname, port string) (string, error) {
	if port == "" {
		return hostname, nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("the port must be between 1 and 65535 (got %q)", port)
	}
	if n == 443 {
		return hostname, nil
	}
	return net.JoinHostPort(hostname, strconv.Itoa(n)), nil
}

// FetchRPMKeys downloads repo's repomd.xml.key and returns its keys only if
// they are exactly the ones the repository's settings select, so a key the
// management API does not vouch for is never trusted.
//
// token is an exchanged Artifact Registry token. httpClient must not add
// credentials of its own. FetchRPMKeys follows no redirect, so the token
// never leaves repo's host.
func FetchRPMKeys(ctx context.Context, httpClient *http.Client, repo *RPMRepository, token string) (*PublicKeys, error) {
	noRedirect := *httpClient
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client := NewRegistryClient(&noRedirect, token)

	keys, err := fetchRPMKeys(ctx, client, repo)
	// A rotation step can land between the two reads, so one mismatch is
	// retried before it is reported.
	if _, mismatch := errors.AsType[*KeyMismatchError](err); mismatch {
		keys, err = fetchRPMKeys(ctx, client, repo)
	}
	return keys, err
}

func fetchRPMKeys(ctx context.Context, client *RegistryClient, repo *RPMRepository) (*PublicKeys, error) {
	selected, err := selectedFingerprints(ctx, client, repo)
	if err != nil {
		return nil, err
	}
	keyURL := repo.BaseURL().JoinPath("repodata", "repomd.xml.key")
	armored, err := client.get(ctx, keyURL, func(r *http.Request) { r.SetBasicAuth("token", client.token) })
	if err != nil {
		return nil, err
	}
	keys, err := CheckPublicKeys(armored, selected)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyURL.Redacted(), err)
	}
	return keys, nil
}

func selectedFingerprints(ctx context.Context, client *RegistryClient, repo *RPMRepository) ([]string, error) {
	base := &url.URL{Scheme: "https", Host: repo.host}
	selected, err := client.SigningKeyFingerprints(ctx, base, repo.handle, repo.name)
	if err != nil {
		return nil, err
	}
	if selected.Format != "rpm" || selected.Kind != "hosted" {
		return nil, fmt.Errorf("%s is a %s %s repository, not a hosted rpm one", repo.BaseURL().Redacted(), selected.Kind, selected.Format)
	}
	for _, f := range selected.List() {
		if !fingerprintRe.MatchString(f) {
			return nil, fmt.Errorf("%s: the repository selects %q, which is not an uppercase 40-character hexadecimal fingerprint", repo.BaseURL().Redacted(), f)
		}
	}
	return selected.List(), nil
}
