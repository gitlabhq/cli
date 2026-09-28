//go:build !integration

package stackutils

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/git"
)

func TestBranchPrefixFromCurrentUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		currentUser func() (*user.User, error)
		want        string
	}{
		{
			name: "uses current username",
			currentUser: func() (*user.User, error) {
				return &user.User{Username: "testuser"}, nil
			},
			want: "testuser",
		},
		{
			name: "removes Windows domain",
			currentUser: func() (*user.User, error) {
				return &user.User{Username: `DOMAIN\windowsuser`}, nil
			},
			want: "windowsuser",
		},
		{
			name: "falls back when username is empty",
			currentUser: func() (*user.User, error) {
				return &user.User{}, nil
			},
			want: "glab-stack",
		},
		{
			name: "falls back when lookup fails",
			currentUser: func() (*user.User, error) {
				return nil, errors.New("lookup failed")
			},
			want: "glab-stack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, branchPrefixFromCurrentUser(tt.currentUser))
		})
	}
}

func TestCheckNoRebaseInProgress(t *testing.T) {
	stackRefs := map[string]git.StackRef{
		"ref-a": {SHA: "ref-a", Prev: "", Next: "", Branch: "branchA"},
	}

	t.Run("clean repo returns nil", func(t *testing.T) {
		git.InitGitRepo(t)
		require.NoError(t, git.CreateRefFiles(stackRefs, "test-stack"))
		require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))

		require.NoError(t, CheckNoRebaseInProgress())
	})

	t.Run("paused reorder is blocked", func(t *testing.T) {
		git.InitGitRepo(t)
		require.NoError(t, git.CreateRefFiles(stackRefs, "test-stack"))
		require.NoError(t, git.SetLocalConfig("glab.currentstack", "test-stack"))
		require.NoError(t, git.WriteReorderState("test-stack",
			git.ReorderState{NewOrder: []string{"branchA"}}))

		err := CheckNoRebaseInProgress()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stack reorder is in progress")
	})

	t.Run("in-progress git rebase is blocked", func(t *testing.T) {
		git.InitGitRepo(t)

		gitDir, err := git.GitDir()
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o755))

		err = CheckNoRebaseInProgress()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Git rebase is currently in progress")
	})
}
