package cmdutils

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// jqCmdHarness builds a minimal cobra.Command with an --output flag, wires
// AddJQFlag onto it, and returns the command plus the captured IOStreams.
func jqCmdHarness(t *testing.T) (*cobra.Command, *iostreams.IOStreams, *bool) {
	t.Helper()
	io := &iostreams.IOStreams{
		StdOut: &bytes.Buffer{},
		StdErr: &bytes.Buffer{},
		JQ:     &iostreams.JQFilter{},
	}
	ran := false
	cmd := &cobra.Command{
		Use: "test",
		RunE: func(*cobra.Command, []string) error {
			ran = true
			return nil
		},
	}
	var outputFormat string
	cmd.Flags().VarP(NewEnumValue([]string{"text", "json"}, "text", &outputFormat), "output", "F", "")
	AddJQFlag(cmd, io)
	return cmd, io, &ran
}

func TestAddJQFlag_NoFlagPassed_FilterUnset(t *testing.T) {
	cmd, io, ran := jqCmdHarness(t)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())
	assert.True(t, *ran)
	assert.False(t, io.JQ.IsActive())
}

func TestAddJQFlag_WithJSONOutput_SetsFilterAndRuns(t *testing.T) {
	cmd, io, ran := jqCmdHarness(t)
	cmd.SetArgs([]string{"--output", "json", "--jq", ".foo"})
	require.NoError(t, cmd.Execute())
	assert.True(t, *ran)
	assert.True(t, io.JQ.IsActive())
	assert.Equal(t, ".foo", io.JQ.String())
}

func TestAddJQFlag_WithoutJSONOutput_FailsBeforeRun(t *testing.T) {
	cmd, _, ran := jqCmdHarness(t)
	// silence cobra's auto-printed error/usage so the test buffer stays clean
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--jq", ".foo"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "using --jq requires --output=json")
	assert.False(t, *ran, "command body should not have run")
}

func TestAddJQFlag_WithExplicitTextOutput_FailsBeforeRun(t *testing.T) {
	cmd, _, ran := jqCmdHarness(t)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--output", "text", "--jq", ".foo"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "got \"text\"")
	assert.False(t, *ran)
}

func TestAddJQFlag_InvalidExpression_FailsAtFlagParse(t *testing.T) {
	cmd, _, ran := jqCmdHarness(t)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--output", "json", "--jq", ".["})

	err := cmd.Execute()
	require.Error(t, err)
	// Error originates from JQFilter.Set during cobra's flag parsing,
	// surfaced as a cobra flag-parse error containing our text.
	assert.Contains(t, err.Error(), "invalid --jq expression")
	assert.False(t, *ran)
}

func TestAddJQFlag_PreservesOriginalPreRunE(t *testing.T) {
	io := &iostreams.IOStreams{
		StdOut: &bytes.Buffer{},
		StdErr: &bytes.Buffer{},
		JQ:     &iostreams.JQFilter{},
	}
	preRanCount := 0
	cmd := &cobra.Command{
		Use:     "test",
		PreRunE: func(*cobra.Command, []string) error { preRanCount++; return nil },
		RunE:    func(*cobra.Command, []string) error { return nil },
	}
	var outputFormat string
	cmd.Flags().VarP(NewEnumValue([]string{"text", "json"}, "text", &outputFormat), "output", "F", "")
	AddJQFlag(cmd, io)

	cmd.SetArgs([]string{"--output", "json", "--jq", ".foo"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, 1, preRanCount, "original PreRunE should run after the jq check")
}

func TestAddJQFlag_PreservesOriginalPreRun(t *testing.T) {
	// Same as the PreRunE case but for the non-E variant. AddJQFlag must
	// fold an existing PreRun into its wrapper instead of dropping it.
	io := &iostreams.IOStreams{
		StdOut: &bytes.Buffer{},
		StdErr: &bytes.Buffer{},
		JQ:     &iostreams.JQFilter{},
	}
	preRanCount := 0
	cmd := &cobra.Command{
		Use:    "test",
		PreRun: func(*cobra.Command, []string) { preRanCount++ },
		RunE:   func(*cobra.Command, []string) error { return nil },
	}
	var outputFormat string
	cmd.Flags().VarP(NewEnumValue([]string{"text", "json"}, "text", &outputFormat), "output", "F", "")
	AddJQFlag(cmd, io)

	cmd.SetArgs([]string{"--output", "json", "--jq", ".foo"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, 1, preRanCount, "original PreRun should still be invoked")
}

func TestAddJQFlag_RegistersNoFileCompletions(t *testing.T) {
	cmd, _, _ := jqCmdHarness(t)

	compFn, ok := cmd.GetFlagCompletionFunc("jq")
	require.True(t, ok, "--jq should have a completion function registered")

	comps, directive := compFn(cmd, nil, "")
	assert.Empty(t, comps)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestAddJQFlag_LooksUpOutputFormatFlag(t *testing.T) {
	io := &iostreams.IOStreams{
		StdOut: &bytes.Buffer{},
		StdErr: &bytes.Buffer{},
		JQ:     &iostreams.JQFilter{},
	}
	cmd := &cobra.Command{
		Use:  "test",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	var outputFormat string
	cmd.Flags().VarP(NewEnumValue([]string{"text", "json"}, "text", &outputFormat), "output-format", "F", "")
	AddJQFlag(cmd, io)

	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--jq", ".foo"})
	err := cmd.Execute()
	require.Error(t, err)
	// Error references the fallback flag name when --output isn't present.
	assert.Contains(t, err.Error(), "--output-format=json")
}

func TestAddJQFlag_AlwaysJSONCommand_SkipsOutputFlagCheck(t *testing.T) {
	// A command without --output / --output-format (e.g. orbit/*) always
	// emits JSON. AddJQFlag must not error when --jq is supplied without
	// any output flag in scope.
	io := &iostreams.IOStreams{
		StdOut: &bytes.Buffer{},
		StdErr: &bytes.Buffer{},
		JQ:     &iostreams.JQFilter{},
	}
	ran := false
	cmd := &cobra.Command{
		Use:  "test",
		RunE: func(*cobra.Command, []string) error { ran = true; return nil },
	}
	AddJQFlag(cmd, io)

	cmd.SetArgs([]string{"--jq", ".foo"})
	require.NoError(t, cmd.Execute())
	assert.True(t, ran)
	assert.True(t, io.JQ.IsActive())
}
