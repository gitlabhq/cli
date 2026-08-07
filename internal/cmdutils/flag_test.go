//go:build !integration

package cmdutils

import (
	"bytes"
	"io"
	"sync"
	"testing"

	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

type options struct {
	group    string
	baseRepo func() (glrepo.Interface, error)
}

type dummyFactory struct {
	baseRepo glrepo.Interface
}

func (f *dummyFactory) RepoOverride(repo string) error {
	baseRepo, err := glrepo.FromFullName(repo, glinstance.DefaultHostname, config.NewBlankConfig())
	if err != nil {
		return err
	}
	f.baseRepo = baseRepo
	return nil
}

func (f *dummyFactory) ApiClient(repoHost string) (*api.Client, error) {
	return nil, nil
}

func (f *dummyFactory) GitLabClient() (*gitlab.Client, error) { return nil, nil }

func (f *dummyFactory) BaseRepo() (glrepo.Interface, error) {
	if f.baseRepo != nil {
		return f.baseRepo, nil
	}

	return glrepo.New("OWNER", "REPO", glinstance.DefaultHostname), nil
}

func (f *dummyFactory) Remotes() (glrepo.Remotes, error) { return nil, nil }

func (f *dummyFactory) Config() config.Config { return config.NewBlankConfig() }

func (f *dummyFactory) Branch() (string, error) { return "", nil }

func (f *dummyFactory) IO() *iostreams.IOStreams { return nil }

func (f *dummyFactory) DefaultHostname() string { return "" }

func (f *dummyFactory) BuildInfo() api.BuildInfo { return api.BuildInfo{} }

func (f *dummyFactory) Executor() Executor { return nil }

func (f *dummyFactory) GitRunner() git.GitRunner { return git.StandardGitCommand{} }

func NewDummyCmd(f Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		baseRepo: f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List objects",
		Long:  "List objects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}

			return nil
		},
	}
	EnableRepoOverride(cmd, f)
	cmd.PersistentFlags().StringP("group", "g", "", "Select another group or it's subgroups")

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	group, err := GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	return nil
}

func runCommand(cli string, runE func(opts *options) error) error {
	cmd := NewDummyCmd(&dummyFactory{}, runE)

	argv, err := shlex.Split(cli)
	if err != nil {
		return err
	}

	cmd.SetArgs(argv)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	_, err = cmd.ExecuteC()
	return err
}

func TestGroupOverride(t *testing.T) {
	// set GITLAB_GROUP environment variable
	t.Setenv("GITLAB_GROUP", "GROUP/NAME")

	t.Run("List command with GITLAB_GROUP env var and group", func(t *testing.T) {
		gotOpts := &options{}

		err := runCommand("--group GROUP/OVERRIDE", func(opts *options) error {
			gotOpts = opts
			return nil
		})

		require.NoError(t, err)
		gotGroup := gotOpts.group
		// make sure the GITLAB_GROUP env variable is not used
		assert.NotEqual(t, "GROUP/NAME", gotGroup)
		// make sure the group flag was used instead
		assert.Equal(t, "GROUP/OVERRIDE", gotGroup)
	})

	t.Run("List command with GITLAB_GROUP env var and no group flag", func(t *testing.T) {
		gotOpts := &options{}

		err := runCommand("", func(opts *options) error {
			gotOpts = opts
			return nil
		})

		require.NoError(t, err)
		gotGroup := gotOpts.group
		// make sure the GITLAB_GROUP env variable is used
		assert.Equal(t, "GROUP/NAME", gotGroup)
	})

	t.Run("List command with GITLAB_GROUP env var and repo flag", func(t *testing.T) {
		gotOpts := &options{}

		err := runCommand("--repo OWNER2/REPO2", func(opts *options) error {
			gotOpts = opts
			return nil
		})

		require.NoError(t, err)
		gotGroup := gotOpts.group
		// make sure the GITLAB_GROUP env variable is not used
		assert.Empty(t, gotGroup)
		// make sure the repo option is used instead
		gotRepo, _ := gotOpts.baseRepo()
		assert.Equal(t, "OWNER2/REPO2", gotRepo.FullName())
	})

	t.Run("List command with GITLAB_GROUP env var and base repo but no repo flag", func(t *testing.T) {
		gotOpts := &options{}

		err := runCommand("", func(opts *options) error {
			gotOpts = opts
			return nil
		})

		require.NoError(t, err)
		gotGroup := gotOpts.group
		// make sure the GITLAB_GROUP env variable is used
		assert.Equal(t, "GROUP/NAME", gotGroup)
		// make sure the default baserepo is still used
		gotRepo, _ := gotOpts.baseRepo()
		assert.Equal(t, "OWNER/REPO", gotRepo.FullName())
	})

	t.Run("concurrent calls do not race", func(t *testing.T) {
		var wg sync.WaitGroup
		const n = 50
		wg.Add(n)

		for range n {
			go func() {
				defer wg.Done()
				cmd := NewDummyCmd(&dummyFactory{}, nil)
				_ = cmd.ParseFlags([]string{})
				group, err := GroupOverride(cmd)
				assert.NoError(t, err)
				assert.Equal(t, "GROUP/NAME", group)
			}()
		}

		wg.Wait()
	})

	t.Run("List command with GITLAB_GROUP env var, repo and groups flags", func(t *testing.T) {
		gotOpts := &options{}

		err := runCommand("--repo OWNER2/REPO2 --group GROUP/OVERRIDE", func(opts *options) error {
			gotOpts = opts
			return nil
		})

		require.NoError(t, err)
		gotGroup := gotOpts.group
		// make sure no group option is set at all (group settings ignored)
		assert.Empty(t, gotGroup)
		// make sure the repo flag is used instead
		gotRepo, _ := gotOpts.baseRepo()
		assert.Equal(t, "OWNER2/REPO2", gotRepo.FullName())
	})
}
