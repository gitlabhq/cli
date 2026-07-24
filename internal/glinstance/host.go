package glinstance

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	DefaultHostname = "gitlab.com"
	DefaultProtocol = "https"
	DefaultClientID = "41d48f9422ebd655dd9cf2947d6979681dfaddc6d0c56f7628f6ada59559af1e"
)

// IsSelfHosted reports whether a non-normalized host name looks like a GitLab Self-Managed instance
// staging.gitlab.com is considered self-managed
func IsSelfHosted(h string) bool {
	return NormalizeHostname(h) != DefaultHostname
}

// NormalizeHostname returns the canonical host name of a GitLab instance
// Note: GitLab does not allow subdomains on gitlab.com https://gitlab.com/gitlab-org/gitlab/-/issues/26703
func NormalizeHostname(h string) string {
	return strings.ToLower(h)
}

// StripHostProtocol strips the url protocol and returns the hostname and the protocol
func StripHostProtocol(h string) (string, string) {
	hostname := NormalizeHostname(h)
	var protocol string
	if strings.HasPrefix(hostname, "http://") {
		protocol = "http"
	} else {
		protocol = "https"
	}
	hostname = strings.TrimPrefix(hostname, protocol)
	hostname = strings.Trim(hostname, ":/")
	return hostname, protocol
}

// ExtractSubfolder splits a hostname string into hostname and optional subfolder.
// Trims slashes from the subfolder result.
// Example: "example.com/gitlab/" → ("example.com", "gitlab")
func ExtractSubfolder(input string) (string, string) {
	parts := strings.SplitN(input, "/", 2)
	hostname := parts[0]
	subfolder := ""
	if len(parts) == 2 {
		subfolder = strings.Trim(parts[1], "/")
	}
	return hostname, subfolder
}

// buildURLPath constructs the URL path with optional subfolder.
func buildURLPath(hostname, subfolder string) string {
	if subfolder == "" {
		return hostname
	}
	return path.Join(hostname, subfolder)
}

// resolveHostAndSubfolder resolves the effective hostname and subfolder from the provided parameters.
// Handles backward compatibility with api_host containing paths.
// Returns (baseHost, effectiveSubfolder).
func resolveHostAndSubfolder(hostname, apiHost, subfolder string) (string, string) {
	baseHost := hostname
	effectiveSubfolder := subfolder

	// Backward compatibility: extract subfolder from api_host
	if effectiveSubfolder == "" && apiHost != "" {
		extractedHost, extractedSubfolder := ExtractSubfolder(apiHost)
		baseHost = extractedHost
		effectiveSubfolder = extractedSubfolder
	} else if apiHost != "" && !strings.Contains(apiHost, "/") {
		// api_host is alternate hostname without subfolder
		baseHost = apiHost
	}

	return baseHost, effectiveSubfolder
}

// APIEndpoint returns the REST API endpoint prefix for a GitLab instance.
//
// Parameters:
//
//	hostname: Base hostname (e.g., "example.com")
//	protocol: "http" or "https"
//	apiHost: (Deprecated) Alternate API hostname, may contain path for backward compat
//	subfolder: Subfolder path (e.g., "gitlab" for https://example.com/gitlab/)
//
// Precedence: subfolder parameter > path in apiHost > none
func APIEndpoint(hostname, protocol, apiHost, subfolder string) string {
	if protocol == "" {
		protocol = DefaultProtocol
	}

	baseHost, effectiveSubfolder := resolveHostAndSubfolder(hostname, apiHost, subfolder)
	urlPath := buildURLPath(baseHost, effectiveSubfolder)

	if IsSelfHosted(baseHost) {
		return fmt.Sprintf("%s://%s/api/v4/", protocol, urlPath)
	}
	return "https://gitlab.com/api/v4/"
}

// GraphQLEndpoint returns the GraphQL API endpoint prefix for a GitLab instance.
// Parameters match APIEndpoint for consistency.
func GraphQLEndpoint(hostname, protocol, apiHost, subfolder string) string {
	if protocol == "" {
		protocol = "https"
	}

	baseHost, effectiveSubfolder := resolveHostAndSubfolder(hostname, apiHost, subfolder)
	urlPath := buildURLPath(baseHost, effectiveSubfolder)

	if IsSelfHosted(baseHost) {
		return fmt.Sprintf("%s://%s/api/graphql/", protocol, urlPath)
	}
	return "https://gitlab.com/api/graphql/"
}

func HostnameValidator(v any) error {
	hostname, valid := v.(string)
	if !valid {
		return errors.New("hostname is not a string")
	}

	if len(strings.TrimSpace(hostname)) < 1 {
		return errors.New("a value is required")
	}
	if strings.ContainsRune(hostname, '/') || strings.ContainsRune(hostname, ':') {
		return errors.New("invalid hostname")
	}
	return nil
}
