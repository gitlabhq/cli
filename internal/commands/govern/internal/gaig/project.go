package gaig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
)

// ErrNotGitLab is returned for a remote whose host glab is not logged in to.
var ErrNotGitLab = errors.New("repository is not on a GitLab host glab is logged in to")

// ProjectForRemote returns the GitLab host and project path of a Git remote
// URL, if the host is one glab is logged in to.
func ProjectForRemote(remote string, hosts []string, cfg config.Config) (string, string, error) {
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
		return "", "", ErrNotGitLab
	}
	return repo.RepoHost(), repo.FullName(), nil
}

// OriginRemote returns the origin remote URL of the repository at dir. It
// reads only standard output, so that a warning git prints, such as about
// the ownership of dir, cannot become part of the URL.
func OriginRemote(ctx context.Context, executor cmdutils.Executor, dir string) (string, error) {
	var out bytes.Buffer
	err := executor.ExecWithIO(ctx, "git", []string{"-C", dir, "config", "--get", "remote.origin.url"}, nil, nil, &out, io.Discard)
	if err != nil {
		return "", fmt.Errorf("could not read the origin remote of %s: %w", dir, err)
	}
	return strings.TrimSpace(out.String()), nil
}

// TopLevel returns the top-level folder of the Git work tree that dir is in.
func TopLevel(ctx context.Context, executor cmdutils.Executor, dir string) (string, error) {
	var out bytes.Buffer
	err := executor.ExecWithIO(ctx, "git", []string{"-C", dir, "rev-parse", "--show-toplevel"}, nil, nil, &out, io.Discard)
	if err != nil {
		return "", fmt.Errorf("could not find the repository of %s: %w", dir, err)
	}
	return strings.TrimSpace(out.String()), nil
}

// ProjectForDir returns the GitLab host and project path of the origin remote
// of the repository at dir, which need not be the working directory.
func ProjectForDir(ctx context.Context, executor cmdutils.Executor, cfg config.Config, dir string) (string, string, error) {
	remote, err := OriginRemote(ctx, executor, dir)
	if err != nil {
		return "", "", err
	}
	hosts, err := cfg.Hosts()
	if err != nil {
		return "", "", fmt.Errorf("could not read configured GitLab hosts: %w", err)
	}
	return ProjectForRemote(remote, hosts, cfg)
}
