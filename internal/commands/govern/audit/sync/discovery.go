package sync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
)

// discoveredSession is a session found by scanning an agent's transcripts
// rather than recorded by a hook.
type discoveredSession struct {
	id      string
	locator string
	// remote is the Git remote URL of the repository the session ran in, or
	// empty when it is unknown.
	remote string
	// skip explains why the session must not be synced, for example because
	// its repository cannot be identified with certainty.
	skip string
}

// discoverSessions records metadata for sessions of the enabled agents that
// no hook or earlier run has recorded, and returns their IDs. A session is
// skipped when its repository is not on a GitLab host glab is logged in to.
func discoverSessions(ctx context.Context, agents []agent, known map[string]struct{}, cfg config.Config, recordErr func(error), recordSkip func(string)) []string {
	enabled, err := fallbacksync.DiscoveredAgents()
	if err != nil {
		recordErr(err)
		return nil
	}
	if len(enabled) == 0 {
		return nil
	}
	hosts, err := cfg.Hosts()
	if err != nil {
		recordErr(fmt.Errorf("could not read configured GitLab hosts: %w", err))
		return nil
	}
	isKnown := func(id string) bool {
		_, ok := known[id]
		return ok
	}

	var found []string
	for _, a := range agents {
		if !slices.Contains(enabled, a.name()) {
			continue
		}
		sessions, err := a.discover(ctx, isKnown)
		if err != nil {
			recordErr(fmt.Errorf("%s discovery: %w", a.name(), err))
			continue
		}
		for _, s := range sessions {
			if s.skip != "" {
				recordSkip(s.skip)
				continue
			}
			host, path, err := projectForRemote(s.remote, hosts, cfg)
			if err != nil {
				dbg.Debugf("not syncing %s session %s: %v", a.name(), s.id, err)
				continue
			}
			err = writeSessionMeta(s.id, sessionMeta{
				AgentType:         a.name(),
				PathWithNamespace: path,
				Host:              host,
				Transcript:        s.locator,
			})
			if err != nil {
				recordErr(fmt.Errorf("session %s: %w", s.id, err))
				continue
			}
			found = append(found, s.id)
		}
	}
	return found
}

var errNotGitLab = errors.New("repository is not on a GitLab host glab is logged in to")

// projectForRemote returns the GitLab host and project path of a Git remote
// URL, if the host is one glab is logged in to.
func projectForRemote(remote string, hosts []string, cfg config.Config) (string, string, error) {
	if remote == "" {
		return "", "", errors.New("repository has no Git remote")
	}
	u, err := git.ParseURL(remote)
	if err != nil {
		return "", "", err
	}
	repo, err := glrepo.FromURL(u, glinstance.DefaultHostname, cfg)
	if err != nil {
		return "", "", err
	}
	if !slices.Contains(hosts, repo.RepoHost()) {
		return "", "", errNotGitLab
	}
	return repo.RepoHost(), repo.FullName(), nil
}

// gitRemote returns the origin remote URL of the repository at dir.
func gitRemote(ctx context.Context, executor cmdutils.Executor, dir string) (string, error) {
	out, err := executor.ExecWithCombinedOutput(ctx, "git", []string{"-C", dir, "config", "--get", "remote.origin.url"}, nil)
	if err != nil {
		return "", fmt.Errorf("could not read the origin remote of %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}
