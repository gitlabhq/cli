//go:build !integration

package gaig

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestProjectForRemote(t *testing.T) {
	t.Parallel()

	cfg := config.NewFromString("hosts:\n  gitlab.example.com:\n    token: x\n")
	hosts := []string{"gitlab.example.com"}

	for _, remote := range []string{
		"git@gitlab.example.com:group/sub/project.git",
		"https://gitlab.example.com/group/sub/project.git",
		"ssh://git@gitlab.example.com:2222/group/sub/project.git",
	} {
		host, path, err := ProjectForRemote(remote, hosts, cfg)
		require.NoError(t, err, remote)
		assert.Equal(t, "gitlab.example.com", host, remote)
		assert.Equal(t, "group/sub/project", path, remote)
	}

	_, _, err := ProjectForRemote("git@github.com:someone/repo.git", hosts, cfg)
	require.ErrorIs(t, err, ErrNotGitLab)

	_, _, err = ProjectForRemote("", hosts, cfg)
	require.Error(t, err)
}

func TestProjectForDir(t *testing.T) {
	t.Parallel()

	cfg := config.NewFromString("hosts:\n  gitlab.example.com:\n    token: x\n")

	t.Run("reads the origin remote of the folder from standard output", func(t *testing.T) {
		t.Parallel()
		executor := cmdtest.NewMockExecutor(gomock.NewController(t))
		executor.EXPECT().
			ExecWithIO(gomock.Any(), "git", []string{"-C", "/work/project", "config", "--get", "remote.origin.url"}, nil, nil, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, stderr io.Writer) error {
				if _, err := io.WriteString(stderr, "warning: not part of the URL\n"); err != nil {
					return err
				}
				_, err := io.WriteString(stdout, "git@gitlab.example.com:group/project.git\n")
				return err
			})

		host, path, err := ProjectForDir(t.Context(), executor, cfg, "/work/project")

		require.NoError(t, err)
		assert.Equal(t, "gitlab.example.com", host)
		assert.Equal(t, "group/project", path)
	})

	t.Run("fails when the folder is not a repository", func(t *testing.T) {
		t.Parallel()
		executor := cmdtest.NewMockExecutor(gomock.NewController(t))
		executor.EXPECT().
			ExecWithIO(gomock.Any(), "git", gomock.Any(), nil, nil, gomock.Any(), gomock.Any()).
			Return(errors.New("exit status 128"))

		_, _, err := ProjectForDir(t.Context(), executor, cfg, "/tmp")

		require.ErrorContains(t, err, "could not read the origin remote of /tmp")
	})
}
