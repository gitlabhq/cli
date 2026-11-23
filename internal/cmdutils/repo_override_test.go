//go:build !integration

package cmdutils

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnableRepoOverride_RegistersNoFileCompletions(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	EnableRepoOverride(cmd, &dummyFactory{})

	compFn, ok := cmd.GetFlagCompletionFunc("repo")
	require.True(t, ok, "--repo should have a completion function registered")

	comps, directive := compFn(cmd, nil, "")
	assert.Empty(t, comps)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestAddGlobalRepoOverride_RegistersNoFileCompletions(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	AddGlobalRepoOverride(cmd, &dummyFactory{})

	compFn, ok := cmd.GetFlagCompletionFunc("repo")
	require.True(t, ok, "--repo should have a completion function registered")

	comps, directive := compFn(cmd, nil, "")
	assert.Empty(t, comps)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRepoOverrideValue(t *testing.T) {
	newCmd := func(t *testing.T, args ...string) *cobra.Command {
		t.Helper()
		cmd := &cobra.Command{Use: "test"}
		EnableRepoOverride(cmd, &dummyFactory{})
		require.NoError(t, cmd.ParseFlags(args))
		return cmd
	}

	t.Run("flag", func(t *testing.T) {
		t.Setenv("GITLAB_REPO", "")
		assert.Equal(t, "group/flag", RepoOverrideValue(newCmd(t, "-R", "group/flag")))
	})

	t.Run("environment variable", func(t *testing.T) {
		t.Setenv("GITLAB_REPO", "group/env")
		assert.Equal(t, "group/env", RepoOverrideValue(newCmd(t)))
	})

	t.Run("flag takes precedence over environment variable", func(t *testing.T) {
		t.Setenv("GITLAB_REPO", "group/env")
		assert.Equal(t, "group/flag", RepoOverrideValue(newCmd(t, "--repo", "group/flag")))
	})

	t.Run("not overridden", func(t *testing.T) {
		t.Setenv("GITLAB_REPO", "")
		assert.Empty(t, RepoOverrideValue(newCmd(t)))
	})

	t.Run("command without repo flag falls back to environment variable", func(t *testing.T) {
		t.Setenv("GITLAB_REPO", "group/env")
		assert.Equal(t, "group/env", RepoOverrideValue(&cobra.Command{Use: "test"}))
	})
}
