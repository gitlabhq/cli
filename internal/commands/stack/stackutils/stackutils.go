package stackutils

import (
	"encoding/hex"
	"fmt"
	"os/user"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
)

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
