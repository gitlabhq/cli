package glrepo

import (
	"fmt"
	"net/url"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
)

// Remotes represents a set of git remotes
type Remotes []*Remote

// FindByName returns the first Remote whose name matches the list
func (r Remotes) FindByName(names ...string) (*Remote, error) {
	for _, name := range names {
		for _, rem := range r {
			if rem.Name == name || name == "*" {
				return rem, nil
			}
		}
	}
	return nil, fmt.Errorf("no GitLab remotes found")
}

// UniqueHosts returns a string of unique hostnames
func (r Remotes) UniqueHosts() string {
	unique := make(map[string]bool, r.Len())
	var uniqueHosts []string
	for _, remote := range r {
		if !unique[remote.RepoHost()] {
			uniqueHosts = append(uniqueHosts, remote.RepoHost())
			unique[remote.RepoHost()] = true
		}
	}

	return strings.Join(uniqueHosts, ", ")
}

// FindByRepo returns the first Remote that points to a specific GitLab repository
func (r Remotes) FindByRepo(owner, name string) (*Remote, error) {
	for _, rem := range r {
		if strings.EqualFold(rem.RepoOwner(), owner) && strings.EqualFold(rem.RepoName(), name) {
			return rem, nil
		}
	}
	return nil, fmt.Errorf("no matching remote found")
}

func remoteNameSortScore(name string) int {
	switch strings.ToLower(name) {
	case "upstream":
		return 3
	case "gitlab":
		return 2
	case "origin":
		return 1
	default:
		return 0
	}
}

// https://golang.org/pkg/sort/#Interface
func (r Remotes) Len() int      { return len(r) }
func (r Remotes) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r Remotes) Less(i, j int) bool {
	return remoteNameSortScore(r[i].Name) > remoteNameSortScore(r[j].Name)
}

// Remote represents a git remote mapped to a GitLab repository
type Remote struct {
	*git.Remote
	Repo Interface
}

func (r Remote) Project(client *gitlab.Client) (*gitlab.Project, error) {
	return r.Repo.Project(client)
}

func (r Remote) RepoNamespace() string {
	return r.Repo.RepoNamespace()
}

func (r Remote) RepoGroup() string {
	return r.Repo.RepoGroup()
}

func (r Remote) FullName() string {
	return r.Repo.FullName()
}

// RepoName is the name of the GitLab repository
func (r Remote) RepoName() string {
	return r.Repo.RepoName()
}

// RepoOwner is the name of the GitLab account that owns the repo
func (r Remote) RepoOwner() string {
	return r.Repo.RepoOwner()
}

// RepoHost is the GitLab hostname that the remote points to
func (r Remote) RepoHost() string {
	return r.Repo.RepoHost()
}

// TODO: accept an interface instead of git.RemoteSet
func TranslateRemotes(gitRemotes git.RemoteSet, urlTranslate func(*url.URL) *url.URL, defaultHostname string, cfg config.Config) Remotes {
	remotes := Remotes{}
	for _, r := range gitRemotes {
		var repo Interface
		if r.FetchURL != nil {
			repo, _ = FromURL(urlTranslate(r.FetchURL), defaultHostname, cfg)
		}
		if r.PushURL != nil && repo == nil {
			repo, _ = FromURL(urlTranslate(r.PushURL), defaultHostname, cfg)
		}
		if repo == nil {
			continue
		}
		remotes = append(remotes, &Remote{
			Remote: r,
			Repo:   repo,
		})
	}
	return remotes
}
