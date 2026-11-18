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
