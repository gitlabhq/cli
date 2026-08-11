package config

import "strings"

// ParseDomains splits a comma-separated domain list, the form the
// container_registry_domains and artifact_registry_domains keys store, into
// its trimmed entries. A blank entry, from an empty value, or a leading,
// trailing, or doubled comma, is dropped rather than kept as "": a blank
// domain reaching dockercredhelper.Register would write "": "glab" into the
// user's Docker config.
//
// It lives here rather than beside either caller because both the Docker
// credential helper and a future `glab artifact-registry login` need it, and
// command packages must not import each other.
func ParseDomains(value string) []string {
	var domains []string
	for domain := range strings.SplitSeq(value, ",") {
		if domain = strings.TrimSpace(domain); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains
}
