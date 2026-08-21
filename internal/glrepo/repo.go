package glrepo

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// RemoteURL returns correct git clone URL of a repo
// based on the user's git_protocol preference
func RemoteURL(project *gitlab.Project, protocol string) string {
	if protocol == "ssh" {
		return project.SSHURLToRepo
	}
	return project.HTTPURLToRepo
}

// WikiRemoteURL returns the clone URL for a project's wiki repository.
func WikiRemoteURL(project *gitlab.Project, protocol string) string {
	return strings.TrimSuffix(RemoteURL(project, protocol), ".git") + ".wiki.git"
}

// FullName returns the repo with its namespace (like profclems/glab). Respects group and subgroups names
func FullNameFromURL(remoteURL string) (string, error) {
	parts := strings.Split(remoteURL, "//")

	if len(parts) == 1 {
		// scp-like short syntax (e.g. git@gitlab.com...)
		part := parts[0]
		parts = strings.Split(part, ":")
	} else if len(parts) == 2 {
		// other protocols (e.g. ssh://, http://, git://)
		part := parts[1]
		parts = strings.SplitN(part, "/", 2)
	} else {
		return "", errors.New("cannot parse remote: " + remoteURL)
	}

	if len(parts) != 2 {
		return "", errors.New("cannot parse remote: " + remoteURL)
	}
	repo := parts[1]
	repo = strings.TrimSuffix(repo, "/")
	repo = strings.TrimSuffix(repo, ".git")
	return repo, nil
}

// Interface describes an object that represents a GitLab repository
// Contains methods for these methods representing these placeholders for a
// project path with :host/:group/:namespace/:repo
// RepoHost = :host, RepoOwner = :group/:namespace, RepoNamespace = :namespace,
// FullName = :group/:namespace/:repo, RepoGroup = :group, RepoName = :repo
type Interface interface {
	RepoName() string
	RepoOwner() string
	RepoNamespace() string
	RepoGroup() string
	RepoHost() string
	FullName() string
	Project(*gitlab.Client) (*gitlab.Project, error)
}

// New instantiates a GitLab repository from owner and repo name arguments
func New(owner, repo, defaultHostname string) Interface {
	return NewWithHost(owner, repo, defaultHostname)
}

// NewWithGroup instantiates a GitLab repository from group, namespace and repo name arguments
func NewWithGroup(group, namespace, repo, hostname, defaultHostname string) Interface {
	owner := fmt.Sprintf("%s/%s", group, namespace)
	if hostname == "" {
		return New(owner, repo, defaultHostname)
	}
	return NewWithHost(owner, repo, hostname)
}

// NewWithHost is like New with an explicit host name
func NewWithHost(owner, repo, hostname string) Interface {
	rp := &glRepo{
		owner:    owner,
		name:     repo,
		fullname: fmt.Sprintf("%s/%s", owner, repo),
		hostname: normalizeHostname(hostname),
	}
	if ri := strings.SplitN(owner, "/", 2); len(ri) == 2 {
		rp.group = ri[0]
		rp.namespace = ri[1]
	} else {
		rp.namespace = owner
	}
	return rp
}

// FromFullName extracts the GitLab repository information from the following
// formats: "OWNER/REPO", "HOST/OWNER/REPO", "HOST/GROUP/NAMESPACE/REPO", and a full URL.
func FromFullName(nwo string, defaultHostname string, cfg config.Config) (Interface, error) {
	nwo = strings.TrimSpace(nwo)
	// check if it's a valid git URL and parse it
	if git.IsValidURL(nwo) {
		u, err := git.ParseURL(nwo)
		if err != nil {
			return nil, err
		}
		return FromURL(u, defaultHostname, cfg)
	}
	// check if it is valid URL and parse it
	if utils.IsValidURL(nwo) {
		u, _ := url.Parse(nwo)
		return FromURL(u, defaultHostname, cfg)
	}

	repo := nwo[strings.LastIndex(nwo, "/")+1:]
	nwoWithoutRepo := strings.TrimSuffix(nwo[:strings.LastIndex(nwo, "/")+1], "/")
	parts := strings.SplitN(nwoWithoutRepo, "/", 2)

	if repo == "" {
		return nil, fmt.Errorf(`expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got %q`, nwo)
	}
	if slices.Contains(parts, "") {
		return nil, fmt.Errorf(`expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got %q`, nwo)
	}
	switch len(parts) {
	case 2: // GROUP/NAMESPACE/REPO or HOST/OWNER/REPO or //HOST/GROUP/NAMESPACE/REPO
		// First, checks if the first part matches the default instance host (i.e. gitlab.com) or the
		// overridden default host (mostly from the GITLAB_HOST env variable)
		if parts[0] == glinstance.DefaultHostname || parts[0] == defaultHostname {
			return NewWithHost(parts[1], repo, normalizeHostname(parts[0])), nil
		}
		// Dots (.) are allowed in group names by GitLab.
		// So we check if the first part contains a dot.
		// However, it could be that the user is specifying a hostname but we can't be sure of that
		// So we check in the list of authenticated hosts and see if it matches any
		// if not, we assume it is a group name that contains a dot
		if strings.ContainsRune(parts[0], '.') && cfg != nil {
			var rI Interface
			hosts, _ := cfg.Hosts()
			if slices.Contains(hosts, parts[0]) {
				rI = NewWithHost(parts[1], repo, normalizeHostname(parts[0]))
			}
			if rI != nil {
				return rI, nil
			}
		}
		// if the first part is not a valid URL, and does not match an
		// authenticated hostname then we assume it is in
		// the format GROUP/NAMESPACE/REPO
		return NewWithGroup(parts[0], parts[1], repo, "", defaultHostname), nil
	case 1: // OWNER/REPO
		return New(parts[0], repo, defaultHostname), nil
	default:
		return nil, fmt.Errorf(`expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got %q`, nwo)
	}
}

// FromURL extracts the GitLab repository information from a git remote URL
func FromURL(u *url.URL, defaultHostname string, cfg config.Config) (Interface, error) {
	if u.Hostname() == "" {
		return nil, fmt.Errorf("no hostname detected")
	}

	// Retrieve subfolder from the injected config when available. The lookup is
	// keyed on u.Host, not u.Hostname(): host entries are written with the port
	// (`auth login --hostname example.com:8080`), and RepoHost below carries the
	// port too, so the API client resolves its own settings under the same key.
	// Keying on the port-stripped hostname silently misses every setting for a
	// ported instance.
	var subfolder string
	if cfg != nil {
		subfolder, _ = cfg.Get(u.Host, "subfolder")

		// Backward compatibility: extract subfolder from api_host if subfolder field is empty
		if subfolder == "" {
			apiHost, _ := cfg.Get(u.Host, "api_host")
			if apiHost != "" && strings.Contains(apiHost, "/") {
				parts := strings.SplitN(apiHost, "/", 2)
				if len(parts) == 2 {
					subfolder = parts[1]
				}
			}
		}
	}

	// Simple subfolder removal - much cleaner than old code!
	urlPath := strings.TrimPrefix(u.Path, "/")
	if subfolder != "" {
		subfolderPrefix := strings.Trim(subfolder, "/") + "/"
		if after, ok := strings.CutPrefix(urlPath, subfolderPrefix); ok {
			urlPath = after
		}
	}

	// Standard path parsing
	urlPath = strings.TrimLeft(git.StripDotGit(urlPath), "/")
	if urlPath == "" || !strings.Contains(urlPath, "/") {
		return nil, fmt.Errorf("invalid path: %s", u.Path)
	}

	lastSlash := strings.LastIndex(urlPath, "/")
	repo := urlPath[lastSlash+1:]
	pathWithoutRepo := urlPath[:lastSlash]

	if repo != "" && pathWithoutRepo != "" {
		parts := strings.SplitN(pathWithoutRepo, "/", 2)
		if len(parts) == 1 {
			return NewWithHost(parts[0], repo, u.Host), nil
		}

		if len(parts) == 2 {
			return NewWithGroup(parts[0], parts[1], repo, u.Host, defaultHostname), nil
		}
	}
	return nil, fmt.Errorf("invalid path: %s", u.Path)
}

func normalizeHostname(h string) string {
	return strings.ToLower(strings.TrimPrefix(h, "www."))
}

// IsSame compares two GitLab repositories
func IsSame(a, b Interface) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.FullName(), b.FullName()) &&
		normalizeHostname(a.RepoHost()) == normalizeHostname(b.RepoHost())
}

type glRepo struct {
	group     string
	owner     string
	name      string
	fullname  string
	hostname  string
	namespace string

	project *Project
}

type Project struct {
	*gitlab.Project
	// for cache invalidation
	fullname string
	// for cache invalidation
	hostname string
}

func (r glRepo) Project(client *gitlab.Client) (*gitlab.Project, error) {
	if r.project != nil && r.project.fullname == r.fullname && r.project.hostname == r.hostname {
		return r.project.Project, nil
	}
	p, err := api.GetProject(client, r.fullname)
	if err != nil {
		return nil, err
	}
	r.project = &Project{
		Project:  p,
		fullname: r.fullname,
		hostname: r.hostname,
	}
	return r.project.Project, err
}

// RepoNamespace returns the namespace of the project. Eg. if project path is :group/:namespace:/repo
// RepoNamespace returns the :namespace
func (r glRepo) RepoNamespace() string {
	return r.namespace
}

// RepoGroup returns the group namespace of the project. Eg. if project path is :group/:namespace:/repo
// RepoGroup returns the :group
func (r glRepo) RepoGroup() string {
	return r.group
}

// RepoOwner returns the group and namespace in the form "group/namespace". Returns "namespace" if group is not present
func (r glRepo) RepoOwner() string {
	if r.group != "" {
		return r.group + "/" + r.namespace
	}
	return r.owner
}

// RepoName returns the repo name without the path or namespace.
func (r glRepo) RepoName() string {
	return r.name
}

// RepoHost returns the hostname
func (r glRepo) RepoHost() string {
	return r.hostname
}

// FullName returns the full project path :group/:namespace/:repo or :namespace/:repo if group is not present
func (r glRepo) FullName() string {
	return r.fullname
}
