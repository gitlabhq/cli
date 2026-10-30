package utils

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	glabapi "gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

var workItemRefRE = regexp.MustCompile(`^([^\s#&]+)[#&](\d+)$`)

// parseWorkItemRef returns the host, namespace path, and IID of a work item
// given as an IID (123 or #123), a reference (group/project#123 or group&123),
// or a URL. The host is empty unless ref is a URL. A bare IID resolves against
// defaultPath.
func parseWorkItemRef(ref, defaultPath string, cfg config.Config) (string, string, int64, error) {
	ref = strings.TrimSpace(ref)
	var host string
	path, iidStr := defaultPath, strings.TrimPrefix(ref, "#")

	if u, err := url.Parse(ref); err == nil && (u.Scheme == "https" || u.Scheme == "http") {
		host = u.Host
		// Group URLs first, since ParseIssueFromURL also matches /groups/ paths.
		if group, iid := cmdutils.ParseGroupWorkItemFromURL(ref, cfg); iid != 0 {
			path, iidStr = group, strconv.Itoa(iid)
		} else if iid, repo := cmdutils.ParseIssueFromURL(ref, glinstance.DefaultHostname, cfg); repo != nil {
			path, iidStr = repo.FullName(), strconv.Itoa(iid)
		} else {
			return "", "", 0, fmt.Errorf("invalid work item URL: %q", ref)
		}
	} else if m := workItemRefRE.FindStringSubmatch(ref); m != nil {
		path, iidStr = m[1], m[2]
	}

	iid, err := strconv.ParseInt(iidStr, 10, 64)
	if err != nil || iid <= 0 {
		return "", "", 0, fmt.Errorf("invalid work item: %q, expected an ID, reference, or URL", ref)
	}

	return host, path, iid, nil
}

// apiHostFor returns the host of the API endpoint that a client built for
// host would use, which differs from host when api_host is configured.
func apiHostFor(cfg config.Config, host string) string {
	if cfg == nil {
		return host
	}
	apiHost, _ := cfg.Get(host, "api_host")
	subfolder, _ := cfg.Get(host, "subfolder")
	u, err := url.Parse(glinstance.APIEndpoint(host, "", apiHost, subfolder))
	if err != nil {
		return host
	}
	return u.Host
}

// ResolveParentID returns the global ID of the work item that ref points to.
func ResolveParentID(ctx context.Context, client *gitlab.Client, cfg config.Config, ref, defaultPath string) (int64, error) {
	host, path, iid, err := parseWorkItemRef(ref, defaultPath, cfg)
	if err != nil {
		return 0, cmdutils.FlagError{Err: err}
	}
	// A parent must be on the same instance as its child.
	if host != "" {
		if apiHost := client.BaseURL().Host; !strings.EqualFold(apiHostFor(cfg, host), apiHost) {
			return 0, cmdutils.FlagError{Err: fmt.Errorf("parent work item %q is not on %s", ref, apiHost)}
		}
	}

	wi, _, err := client.WorkItems.GetWorkItem(path, iid, gitlab.WithContext(ctx), glabapi.WithSafeRetries())
	if err != nil {
		return 0, fmt.Errorf("failed to find parent work item %s#%d: %w", path, iid, err)
	}

	return wi.ID, nil
}
