package stackutils

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os/user"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
)

// CheckNoRebaseInProgress returns an error when a stack reorder or a Git rebase
// is currently in progress. Stack commands that mutate branches or commits call
// this up front: Git does not reliably block those operations mid-rebase (for
// example `checkout -b` and `commit --amend` succeed once the index is clean),
// and running them can corrupt the stack. A paused reorder counts as in
// progress even between branches, when no Git rebase is active.
func CheckNoRebaseInProgress() error {
	if title, err := git.GetCurrentStackTitle(); err == nil {
		inProgress, err := git.ReorderInProgress(title)
		if err != nil {
			return fmt.Errorf("could not determine stack reorder state: %w", err)
		}
		if inProgress {
			return errors.New(
				"a stack reorder is in progress; run `glab stack reorder --continue` or `glab stack reorder --abort` before running this command")
		}
	}

	if git.RebaseInProgress() {
		return errors.New(
			"a Git rebase is currently in progress; finish it before running this command.\n" +
				"  Resolve the rebase with `git rebase --continue` (or abort it with `git rebase --abort`)")
	}

	return nil
}

func GenerateStackSha(message string, title string, author string, timestamp time.Time) (string, error) {
	toSha := []byte(message + title + author + timestamp.String())
	hashData := make([]byte, 4)

	shakeHash := sha3.NewShake256()
	shakeHash.Write(toSha)
	_, err := shakeHash.Read(hashData)
	if err != nil {
		return "", fmt.Errorf("error generating hash for stack branch: %w", err)
	}

	return hex.EncodeToString(hashData), nil
}

func CreateShaBranch(f cmdutils.Factory, sha string, title string) (string, error) {
	cfg := f.Config()

	prefix, err := cfg.Get("", "branch_prefix")
	if err != nil {
		return "", fmt.Errorf("could not get prefix config: %w", err)
	}

	if prefix == "" {
		prefix = branchPrefixFromCurrentUser(user.Current)
	}

	branchTitle := []string{prefix, title, sha}
	branch := strings.Join(branchTitle, "-")
	return branch, nil
}

func branchPrefixFromCurrentUser(currentUser func() (*user.User, error)) string {
	u, err := currentUser()
	if err != nil || u == nil {
		return "glab-stack"
	}

	username := u.Username
	if _, unqualified, found := strings.Cut(username, `\`); found {
		username = unqualified
	}
	if username == "" {
		return "glab-stack"
	}
	return username
}

func CommitSubject(gr git.GitRunner, hash string) (string, error) {
	output, err := gr.Git("log", "-1", "--format=%s", hash)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func HasComment(words []string) bool {
	return len(words) > 1 && strings.HasPrefix(words[1], "#")
}
