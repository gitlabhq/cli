package glrepo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/go-multierror"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// cap the number of git remotes looked up, since the user might have an
// unusually large number of git remotes
const maxRemotesForLookup = 5

var (
	resolverBaseRepoQuestion = "Which should be the base repository (used for e.g. querying issues) for this directory?"
	resolverHeadRepoQuestion = "Which should be the head repository (where branches are pushed) for this directory?"
)

func ResolveRemotesToRepos(remotes Remotes, client *gitlab.Client, defaultHostname string, cfg config.Config) (*ResolvedRemotes, error) {
	sort.Stable(remotes)

	result := &ResolvedRemotes{
		remotes:         remotes,
		apiClient:       client,
		defaultHostname: defaultHostname,
		cfg:             cfg,
	}

	return result, nil
}

func resolveNetwork(result *ResolvedRemotes) error {
	// Loop over at most 5 (maxRemotesForLookup)
	var errs error
	anySuccess := false
	for i := 0; i < len(result.remotes) && i < maxRemotesForLookup; i++ {
		networkResult, err := api.GetProject(result.apiClient, result.remotes[i].FullName())
		if err == nil {
			result.network = append(result.network, *networkResult)
			anySuccess = true
		} else {
			errs = multierror.Append(errs, fmt.Errorf("%s: %w", result.remotes[i].FullName(), err))
		}
	}
	if anySuccess {
		return nil
	}
	return errs
}

type ResolvedRemotes struct {
	remotes         Remotes
	network         []gitlab.Project
	apiClient       *gitlab.Client
	defaultHostname string
	cfg             config.Config
}

func (r *ResolvedRemotes) BaseRepo(ctx context.Context, ios *iostreams.IOStreams) (Interface, error) {
	// if any of the remotes already has a resolution, respect that
	for _, remote := range r.remotes {
		// Check new ResolvedBase field first
		if remote.ResolvedBase == "base" {
			return remote, nil
		} else if after, ok := strings.CutPrefix(remote.ResolvedBase, "base:"); ok {
			repo, err := FromFullName(after, r.defaultHostname, r.cfg)
			if err != nil {
				return nil, err
			}
			return NewWithHost(repo.RepoOwner(), repo.RepoName(), remote.RepoHost()), nil
		}

		// Fall back to legacy Resolved field for backward compatibility
		if remote.Resolved == "base" {
			return remote, nil
		} else if after, ok := strings.CutPrefix(remote.Resolved, "base:"); ok {
			repo, err := FromFullName(after, r.defaultHostname, r.cfg)
			if err != nil {
				return nil, err
			}
			return NewWithHost(repo.RepoOwner(), repo.RepoName(), remote.RepoHost()), nil
		} else if remote.Resolved != "" && !strings.HasPrefix(remote.Resolved, "head") {
			// Backward compatibility kludge for remote-less resolutions created before
			// BaseRepo started creating resolutions prefixed with `base:`
			repo, err := FromFullName(remote.Resolved, r.defaultHostname, r.cfg)
			if err != nil {
				return nil, err
			}
			// Rewrite resolution, ignore the error as this will keep working
			// in the future we might add a warning that we couldn't rewrite
			// it for compatibility
			_ = git.SetRemoteResolution(remote.Name, "base:"+remote.Resolved)

			return NewWithHost(repo.RepoOwner(), repo.RepoName(), remote.RepoHost()), nil
		}
	}

	if !ios.PromptEnabled() {
		// we cannot prompt, so just resort to the 1st remote
		return r.remotes[0], nil
	}

	// from here on, consult the API
	if r.network == nil {
		err := resolveNetwork(r)
		if err != nil {
			return nil, err
		}
		if len(r.network) == 0 {
			return nil, errors.New("no GitLab Projects found from remotes")
		}
	}

	var repoNames []string
	repoMap := map[string]*gitlab.Project{}
	add := func(r *gitlab.Project) {
		// Prefer PathWithNamespace from API over parsing URLs to avoid
		// issues with split-host setups where config is keyed differently
		fn := r.PathWithNamespace
		if fn == "" {
			fn, _ = FullNameFromURL(r.HTTPURLToRepo)
		}
		if _, ok := repoMap[fn]; !ok {
			repoMap[fn] = r
			repoNames = append(repoNames, fn)
		}
	}

	for i := range r.network {
		if r.network[i].ForkedFromProject != nil {
			fProject, _ := api.GetProject(r.apiClient, r.network[i].ForkedFromProject.PathWithNamespace)
			add(fProject)
		}
		add(&r.network[i])
	}

	baseName := repoNames[0]
	if len(repoNames) > 1 {
		err := ios.Select(ctx, &baseName, resolverBaseRepoQuestion, repoNames)
		if err != nil {
			return nil, err
		}
	}

	// determine corresponding git remote
	selectedRepo := repoMap[baseName]
	selectedRepoInfo, _ := FromFullName(selectedRepo.HTTPURLToRepo, r.defaultHostname, r.cfg)
	resolution := "base"
	remote, _ := r.RemoteForRepo(selectedRepoInfo)
	if remote == nil {
		remote = r.remotes[0]
		// Prefer PathWithNamespace from API to avoid URL parsing issues
		resolution = selectedRepo.PathWithNamespace
		if resolution == "" {
			resolution, _ = FullNameFromURL(selectedRepo.HTTPURLToRepo)
		}
		resolution = "base:" + resolution
	}

	// cache the result to git config
	err := git.SetRemoteResolution(remote.Name, resolution)

	// Create the final repo object using the remote's host, not the API host
	remoteHost := remote.RepoHost()
	if remoteHost == "" {
		return nil, fmt.Errorf("remote %s has invalid or empty host", remote.Name)
	}
	finalRepo := NewWithHost(selectedRepoInfo.RepoOwner(), selectedRepoInfo.RepoName(), remoteHost)
	return finalRepo, err
}

func (r *ResolvedRemotes) HeadRepo(ctx context.Context, ios *iostreams.IOStreams) (Interface, error) {
	// if any of the remotes already has a resolution, respect that
	for _, remote := range r.remotes {
		// Check new ResolvedHead field first
		if remote.ResolvedHead == "head" {
			return remote, nil
		} else if after, ok := strings.CutPrefix(remote.ResolvedHead, "head:"); ok {
			repo, err := FromFullName(after, r.defaultHostname, r.cfg)
			if err != nil {
				return nil, err
			}
			return NewWithHost(repo.RepoOwner(), repo.RepoName(), remote.RepoHost()), nil
		}

		// Fall back to legacy Resolved field for backward compatibility
		if remote.Resolved == "head" {
			return remote, nil
		} else if after, ok := strings.CutPrefix(remote.Resolved, "head:"); ok {
			repo, err := FromFullName(after, r.defaultHostname, r.cfg)
			if err != nil {
				return nil, err
			}
			return NewWithHost(repo.RepoOwner(), repo.RepoName(), remote.RepoHost()), nil
		}
	}

	// from here on, consult the API
	if r.network == nil {
		err := resolveNetwork(r)
		if err != nil {
			return nil, err
		}
		if len(r.network) == 0 {
			return nil, errors.New("no GitLab Projects found from remotes")
		}
	}

	var repoNames []string
	repoMap := map[string]*gitlab.Project{}
	add := func(r *gitlab.Project) {
		// Prefer PathWithNamespace from API over parsing URLs to avoid
		// issues with split-host setups where config is keyed differently
		fn := r.PathWithNamespace
		if fn == "" {
			fn, _ = FullNameFromURL(r.HTTPURLToRepo)
		}
		if _, ok := repoMap[fn]; !ok {
			repoMap[fn] = r
			repoNames = append(repoNames, fn)
		}
	}

	for i := range r.network {
		if r.network[i].ForkedFromProject != nil {
			fProject, _ := api.GetProject(r.apiClient, r.network[i].ForkedFromProject.PathWithNamespace)
			add(fProject)
		}
		add(&r.network[i])
	}

	headName := repoNames[0]
	if len(repoNames) > 1 {
		if !ios.PromptEnabled() {
			// We cannot prompt so get the first repo that is a fork
			for _, repo := range repoNames {
				if repoMap[repo].ForkedFromProject != nil {
					selectedRepoInfo, _ := FromFullName(repoMap[repo].HTTPURLToRepo, r.defaultHostname, r.cfg)
					remote, _ := r.RemoteForRepo(selectedRepoInfo)
					return remote, nil
				}
			}
			// There are no forked repos so return the first repo
			return r.remotes[0], nil
		}

		err := ios.Select(ctx, &headName, resolverHeadRepoQuestion, repoNames)
		if err != nil {
			return nil, err
		}
	}

	// determine corresponding git remote
	selectedRepo := repoMap[headName]
	selectedRepoInfo, _ := FromFullName(selectedRepo.HTTPURLToRepo, r.defaultHostname, r.cfg)
	resolution := "head"
	remote, _ := r.RemoteForRepo(selectedRepoInfo)
	if remote == nil {
		remote = r.remotes[0]
		// Prefer PathWithNamespace from API to avoid URL parsing issues
		resolution = selectedRepo.PathWithNamespace
		if resolution == "" {
			resolution, _ = FullNameFromURL(selectedRepo.HTTPURLToRepo)
		}
		resolution = "head:" + resolution
	}

	// cache the result to git config
	err := git.SetRemoteResolution(remote.Name, resolution)

	// Create the final repo object using the remote's host, not the API host
	remoteHost := remote.RepoHost()
	if remoteHost == "" {
		return nil, fmt.Errorf("remote %s has invalid or empty host", remote.Name)
	}
	finalRepo := NewWithHost(selectedRepoInfo.RepoOwner(), selectedRepoInfo.RepoName(), remoteHost)
	return finalRepo, err
}

// RemoteForRepo finds the git remote that points to a repository
func (r *ResolvedRemotes) RemoteForRepo(repo Interface) (*Remote, error) {
	for _, remote := range r.remotes {
		if IsSame(remote, repo) {
			return remote, nil
		}
	}
	return nil, errors.New("not found")
}
