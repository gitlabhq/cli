package cmdutils

import (
	"context"
	"fmt"
	"strings"
	"sync"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// Factory is a way to obtain core tools for the commands.
// Safe for concurrent use.
type Factory interface {
	RepoOverride(repo string) error
	ApiClient(repoHost string) (*api.Client, error)
	// GitLabClient returns an HTTP client that is initialize with the host from BaseRepo.
	// You must only use GitLabClient if your command is tied to a single repository,
	// otherwise use ApiClient
	GitLabClient() (*gitlab.Client, error)
	BaseRepo() (glrepo.Interface, error)
	Remotes() (glrepo.Remotes, error)
	Config() config.Config
	Branch() (string, error)
	IO() *iostreams.IOStreams
	DefaultHostname() string
	BuildInfo() api.BuildInfo
	Executor() Executor
	GitRunner() git.GitRunner
}

type DefaultFactory struct {
	io              *iostreams.IOStreams
	config          config.Config
	resolveRepos    bool
	buildInfo       api.BuildInfo
	defaultHostname string
	defaultProtocol string

	mu sync.Mutex // protects the fields below
	// cachedBaseRepo if set is the SSoT of the repository to use in BaseRepo(), GitLabClient() and other factory function that require a repository.
	// This is also being set for a repo override.
	cachedBaseRepo glrepo.Interface
}

func NewFactory(io *iostreams.IOStreams, resolveRepos bool, cfg config.Config, buildInfo api.BuildInfo) *DefaultFactory {
	f := &DefaultFactory{
		io:              io,
		config:          cfg,
		resolveRepos:    resolveRepos,
		buildInfo:       buildInfo,
		defaultHostname: glinstance.DefaultHostname,
		defaultProtocol: glinstance.DefaultProtocol,
	}

	// Fetch the custom host config from env vars, then local config.yml, then global config,yml.
	customGLHost, _ := cfg.Get("", "host")
	if customGLHost != "" {
		if utils.IsValidURL(customGLHost) {
			var protocol string
			customGLHost, protocol = glinstance.StripHostProtocol(customGLHost)
			f.defaultProtocol = protocol
		}
		f.defaultHostname = customGLHost
	}

	baseRepo, err := f.BaseRepo()
	if err == nil {
		f.defaultHostname = baseRepo.RepoHost()
	}

	return f
}

func (f *DefaultFactory) DefaultHostname() string {
	return f.defaultHostname
}

func (f *DefaultFactory) RepoOverride(repo string) error {
	if repo == "" {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	baseRepo, err := glrepo.FromFullName(repo, f.defaultHostname, f.config)
	if err != nil {
		return err // return the error if repo was overridden.
	}
	f.cachedBaseRepo = baseRepo
	return nil
}

func (f *DefaultFactory) ApiClient(repoHost string) (*api.Client, error) {
	if repoHost == "" {
		repoHost = f.defaultHostname
	}
	c, err := api.NewClientFromConfig(repoHost, f.config, false, f.buildInfo.UserAgent())
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (f *DefaultFactory) GitLabClient() (*gitlab.Client, error) {
	// TODO: the above code is a safety net for the factory changes introduced with
	// https://gitlab.com/gitlab-org/cli/-/merge_requests/2181
	// Eventually, we should make the factory independent of the repository a command
	// uses and move that logic into a separate repository resolver.
	// The factory should only be used to create things that to not have a state.
	// Currently, the base repository is treated as state.
	// Use the following code if you want to remove the safety net:
	// repo, err := f.BaseRepo()
	// if err != nil {
	// 	return nil, err
	// }
	var repoHost string
	repo, err := f.BaseRepo()
	switch err {
	case nil:
		repoHost = repo.RepoHost()
	default:
		repoHost = f.defaultHostname
		dbg.Debug("The current command request Factory.GitLabClient() without being able to resolve a base repository. The command should probably use Factory.ApiClient() instead")
	}

	c, err := api.NewClientFromConfig(repoHost, f.config, false, f.buildInfo.UserAgent())
	if err != nil {
		return nil, err
	}

	return c.Lab(), nil
}

func (f *DefaultFactory) BaseRepo() (glrepo.Interface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cachedBaseRepo := f.cachedBaseRepo
	if cachedBaseRepo != nil {
		return cachedBaseRepo, nil
	}

	baseRepo, err := f.resolveBaseRepoFromRemotes(context.Background())
	if err != nil {
		return nil, err
	}

	// cache base repo
	f.cachedBaseRepo = baseRepo
	return f.cachedBaseRepo, nil
}

func (f *DefaultFactory) resolveBaseRepoFromRemotes(ctx context.Context) (glrepo.Interface, error) {
	remotes, err := f.Remotes()
	if err != nil {
		return nil, err
	}

	if !f.resolveRepos {
		return remotes[0], nil
	}

	ac, err := api.NewClientFromConfig(remotes[0].RepoHost(), f.config, false, f.buildInfo.UserAgent())
	if err != nil {
		return nil, err
	}

	repoContext, err := glrepo.ResolveRemotesToRepos(remotes, ac.Lab(), f.defaultHostname, f.config)
	if err != nil {
		return nil, err
	}

	baseRepo, err := repoContext.BaseRepo(ctx, f.io)
	if err != nil {
		return nil, err
	}

	return baseRepo, nil
}

func (f *DefaultFactory) Remotes() (glrepo.Remotes, error) {
	hostOverride := ""
	if !strings.EqualFold(glinstance.DefaultHostname, f.defaultHostname) {
		hostOverride = f.defaultHostname
	}
	rr := &remoteResolver{
		readRemotes:     git.Remotes,
		getConfig:       f.Config,
		parseSSHConfig:  git.ParseSSHConfig,
		defaultHostname: f.defaultHostname,
	}
	fn := rr.Resolver(hostOverride)
	return fn()
}

func (f *DefaultFactory) Config() config.Config {
	return f.config
}

func (f *DefaultFactory) Branch() (string, error) {
	currentBranch, err := git.CurrentBranch()
	if err != nil {
		return "", fmt.Errorf("could not determine current branch: %w", err)
	}
	return currentBranch, nil
}

func (f *DefaultFactory) IO() *iostreams.IOStreams {
	return f.io
}

func (f *DefaultFactory) BuildInfo() api.BuildInfo {
	return f.buildInfo
}

func (f *DefaultFactory) Executor() Executor {
	return &factoryExecutor{
		io: f.io,
	}
}

func (f *DefaultFactory) GitRunner() git.GitRunner {
	return git.NewGitCommand(f.Executor())
}
