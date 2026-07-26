package git

import (
	"net/url"
	"strings"
)

func isSupportedProtocol(u string) bool {
	// URL schemes are case-insensitive (RFC 3986), so compare in lower case.
	// Otherwise an uppercase scheme like "HTTPS://" is not recognized and gets
	// misparsed as scp-style SSH further down.
	u = strings.ToLower(u)
	return strings.HasPrefix(u, "ssh:") ||
		strings.HasPrefix(u, "git+ssh:") ||
		strings.HasPrefix(u, "git:") ||
		strings.HasPrefix(u, "http:") ||
		strings.HasPrefix(u, "https:")
}

func isPossibleProtocol(u string) bool {
	lower := strings.ToLower(u)
	return isSupportedProtocol(lower) ||
		strings.HasPrefix(lower, "ftp:") ||
		strings.HasPrefix(lower, "ftps:") ||
		strings.HasPrefix(lower, "file:")
}

// ParseURL normalizes git remote urls
func ParseURL(rawURL string) (*url.URL, error) {
	if !isPossibleProtocol(rawURL) &&
		strings.ContainsRune(rawURL, ':') &&
		// not a Windows path
		!strings.ContainsRune(rawURL, '\\') {
		// support scp-like syntax for ssh protocol
		rawURL = "ssh://" + strings.Replace(rawURL, ":", "/", 1)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "git+ssh" {
		u.Scheme = "ssh"
	}

	if u.Scheme != "ssh" {
		return u, nil
	}

	if strings.HasPrefix(u.Path, "//") {
		u.Path = strings.TrimPrefix(u.Path, "/")
	}

	if idx := strings.Index(u.Host, ":"); idx >= 0 {
		u.Host = u.Host[0:idx]
	}

	return u, nil
}

// IsValidUrl tests a string to determine if it is a valid Git url or not.
func IsValidURL(u string) bool {
	return strings.HasPrefix(u, "git@") || isSupportedProtocol(u)
}
