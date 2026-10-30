// This file implements URL parsing for GitLab resources to allow commands
// to accept full GitLab URLs instead of just resource IDs.
//
// Supported URL formats:
// - Merge Requests: https://gitlab.com/group/project/-/merge_requests/123
// - Issues: https://gitlab.com/group/project/-/issues/456
// - Self-managed instances: https://invent.kde.org/kde/project/-/merge_requests/42
// - Deep subgroups: https://gitlab.com/group/sub1/sub2/project/-/merge_requests/123
// - URL fragments and query parameters are automatically stripped
package cmdutils

import (
	"net/url"
	"regexp"
	"strconv"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
)

// GitLabResourceType represents the type of GitLab resource
type GitLabResourceType string

const (
	// GitLabResourceMergeRequest represents a merge request resource
	GitLabResourceMergeRequest GitLabResourceType = "merge_request"

	// GitLabResourceIssue represents an issue resource
	GitLabResourceIssue GitLabResourceType = "issue"
)

// GitLabResourceMetadata contains the parsed metadata from a GitLab URL
type GitLabResourceMetadata struct {
	ID   int
	Type GitLabResourceType
	Repo glrepo.Interface
}

// mergeRequestURLPathRE is a regex which matches the following patterns:
//
//	OWNER/REPO/merge_requests/id
//	OWNER/REPO/-/merge_requests/id
//	GROUP/NAMESPACE/REPO/merge_requests/id
//	GROUP/NAMESPACE/REPO/-/merge_requests/id
//	including nested subgroups:
//	GROUP/SUBGROUP/../../REPO/-/merge_requests/id
//
// This pattern avoids backtracking issues by using a more specific pattern
// instead of .* which can cause catastrophic backtracking in edge cases.
var mergeRequestURLPathRE = regexp.MustCompile(`^(/(?:[^-][^/]+/){2,})+(?:-/)?merge_requests/(\d+)(?:/.*)?$`)

// issueURLPathRE is a regex which matches the following patterns:
//
//	OWNER/REPO/issues/id
//	OWNER/REPO/-/issues/id
//	OWNER/REPO/-/issues/incident/id
//	OWNER/REPO/-/work_items/id
//	GROUP/NAMESPACE/REPO/issues/id
//	GROUP/NAMESPACE/REPO/-/issues/id
//	GROUP/NAMESPACE/REPO/-/issues/incident/id
//	GROUP/NAMESPACE/REPO/-/work_items/id
//	including nested subgroups:
//	GROUP/SUBGROUP/../../REPO/-/issues/id
//	GROUP/SUBGROUP/../../REPO/-/issues/incident/id
//	GROUP/SUBGROUP/../../REPO/-/work_items/id
//
// The pattern tolerates trailing path segments (e.g., /issues/1/foo) to align
// with mergeRequestURLPathRE behavior. Note that url.Parse strips query strings
// and fragments into separate fields before regex matching.
var issueURLPathRE = regexp.MustCompile(`^(/(?:[^-][^/]+/){2,})+(?:-/)?(?:issues/(?:incident/)?|work_items/)(\d+)(?:/.*)?$`)

// groupWorkItemURLPathRE is a regex which matches the following patterns:
//
//	groups/GROUP/-/work_items/id
//	groups/GROUP/-/epics/id
//	groups/GROUP/epics/id
//	including nested subgroups:
//	groups/GROUP/SUBGROUP/-/work_items/id
var groupWorkItemURLPathRE = regexp.MustCompile(`^/groups/(.+?)/(?:-/)?(?:work_items|epics)/(\d+)(?:/.*)?$`)

// ParseGitLabURL returns nil when urlStr is not a merge request or issue URL.
// URLs without a scheme are accepted.
func ParseGitLabURL(urlStr, defaultHostname string, cfg config.Config) *GitLabResourceMetadata {
	u := parseResourceURL(urlStr)
	if u == nil {
		return nil
	}

	if m := mergeRequestURLPathRE.FindStringSubmatch(u.Path); m != nil {
		return resourceFromMatch(*u, m, GitLabResourceMergeRequest, defaultHostname, cfg)
	}
	if m := issueURLPathRE.FindStringSubmatch(u.Path); m != nil {
		return resourceFromMatch(*u, m, GitLabResourceIssue, defaultHostname, cfg)
	}
	return nil
}

// ParseMergeRequestFromURL extracts merge request ID and repository from a GitLab URL
// Returns 0 and nil if the URL is not a valid merge request URL
func ParseMergeRequestFromURL(urlStr, defaultHostname string, cfg config.Config) (int, glrepo.Interface) {
	metadata := ParseGitLabURL(urlStr, defaultHostname, cfg)
	if metadata != nil && metadata.Type == GitLabResourceMergeRequest {
		return metadata.ID, metadata.Repo
	}
	return 0, nil
}

// ParseIssueFromURL returns 0 and nil if urlStr is not an issue, incident, or work item URL.
// Unlike ParseGitLabURL, it never resolves the URL as a merge request, so an
// issue URL with a trailing merge_requests segment still parses as an issue.
func ParseIssueFromURL(urlStr, defaultHostname string, cfg config.Config) (int, glrepo.Interface) {
	u := parseResourceURL(urlStr)
	if u == nil {
		return 0, nil
	}

	m := issueURLPathRE.FindStringSubmatch(u.Path)
	if m == nil {
		return 0, nil
	}
	metadata := resourceFromMatch(*u, m, GitLabResourceIssue, defaultHostname, cfg)
	if metadata == nil {
		return 0, nil
	}
	return metadata.ID, metadata.Repo
}

// ParseGroupWorkItemFromURL returns the group path and IID of a group work
// item or epic URL, or "" and 0 if urlStr is not one. The host's configured
// subfolder is ignored.
func ParseGroupWorkItemFromURL(urlStr string, cfg config.Config) (string, int) {
	u := parseResourceURL(urlStr)
	if u == nil {
		return "", 0
	}

	m := groupWorkItemURLPathRE.FindStringSubmatch("/" + glrepo.TrimSubfolder(u, cfg))
	if m == nil {
		return "", 0
	}
	id, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0
	}
	return m[1], id
}

func parseResourceURL(urlStr string) *url.URL {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}
	if u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http" {
		return nil
	}
	return u
}

// resourceFromMatch expects m to be a submatch of mergeRequestURLPathRE or
// issueURLPathRE, where m[1] is the project path and m[2] is the resource ID.
func resourceFromMatch(u url.URL, m []string, resourceType GitLabResourceType, defaultHostname string, cfg config.Config) *GitLabResourceMetadata {
	resourceID, err := strconv.Atoi(m[2])
	if err != nil {
		return nil
	}

	u.Path = m[1]
	repo, err := glrepo.FromURL(&u, defaultHostname, cfg)
	if err != nil {
		return nil
	}

	return &GitLabResourceMetadata{
		ID:   resourceID,
		Type: resourceType,
		Repo: repo,
	}
}
