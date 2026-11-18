package cmdutils

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// outputCmdHarness builds a minimal cobra.Command wired with EnableJSONOutput
// and returns the command, the IOStreams it records onto, and the command-local
// field the flag is bound to.
func outputCmdHarness(t *testing.T) (*cobra.Command, *iostreams.IOStreams, *string) {
	t.Helper()
	ios := &iostreams.IOStreams{
		StdOut: &bytes.Buffer{},
		StdErr: &bytes.Buffer{},
		JQ:     &iostreams.JQFilter{},
	}
	cmd := &cobra.Command{
		Use:          "test <key>",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE:         func(*cobra.Command, []string) error { return nil },
	}
	var outputFormat string
	EnableJSONOutput(cmd, ios, &outputFormat)
	return cmd, ios, &outputFormat
}

func TestEnableJSONOutput_NoFlagPassed_FormatNotRecorded(t *testing.T) {
	cmd, ios, outputFormat := outputCmdHarness(t)
	cmd.SetArgs([]string{"KEY"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, "text", *outputFormat)
	assert.False(t, ios.IsJSONOutput())
}

func TestEnableJSONOutput_JSONFlagPassed_RecordsFormatOnIOStreams(t *testing.T) {
	cmd, ios, outputFormat := outputCmdHarness(t)
	cmd.SetArgs([]string{"KEY", "--output", "json"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, "json", *outputFormat)
	assert.True(t, ios.IsJSONOutput())
}

func TestEnableJSONOutput_TextFlagPassed_RecordsFormatOnIOStreams(t *testing.T) {
	cmd, ios, outputFormat := outputCmdHarness(t)
	cmd.SetArgs([]string{"KEY", "-F", "text"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, "text", *outputFormat)
	assert.False(t, ios.IsJSONOutput())
}

func TestEnableJSONOutput_ArgValidationFails_FormatStillRecorded(t *testing.T) {
	// Flags are parsed before Args validation, so a command that never
	// reaches RunE has still recorded the format the user asked for. This is
	// what lets NewGitLabErrorHandler report such failures as JSON.
	cmd, ios, _ := outputCmdHarness(t)
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"-F", "json"})

	require.Error(t, cmd.Execute())
	assert.True(t, ios.IsJSONOutput())
}

func TestEnableJSONOutput_InvalidFormat_RejectedAndNotRecorded(t *testing.T) {
	cmd, ios, _ := outputCmdHarness(t)
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"KEY", "-F", "yaml"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be one of")
	assert.False(t, ios.IsJSONOutput())
}

func TestEnableJSONOutput_CustomDescription(t *testing.T) {
	// The enum only ever accepts "text"/"json" regardless of this string —
	// it's passthrough flag help text, not a third format. Deliberately
	// avoid a plausible-looking format name here so the test data can't be
	// misread as implying broader support.
	cmd, ios, _ := outputCmdHarness(t)
	assert.Equal(t, "Format output as: text, json.", cmd.Flag("output").Usage)

	var outputFormat string
	other := &cobra.Command{Use: "other", RunE: func(*cobra.Command, []string) error { return nil }}
	EnableJSONOutput(other, ios, &outputFormat, "Custom help text for this command's --output flag.")
	assert.Equal(t, "Custom help text for this command's --output flag.", other.Flag("output").Usage)
}

func TestEnableJSONOutput_RegistersJQFlag(t *testing.T) {
	cmd, _, _ := outputCmdHarness(t)
	assert.NotNil(t, cmd.Flag("jq"), "EnableJSONOutput must also register --jq")
}

func TestEnableJSONOutput_RegistersFlagCompletions(t *testing.T) {
	cmd, _, _ := outputCmdHarness(t)

	compFn, ok := cmd.GetFlagCompletionFunc("output")
	require.True(t, ok, "--output should have a completion function registered")

	comps, directive := compFn(cmd, nil, "")
	assert.Equal(t, []cobra.Completion{"text", "json"}, comps)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestEnableJSONOutputP_CustomShorthand(t *testing.T) {
	ios := &iostreams.IOStreams{
		StdOut: &bytes.Buffer{},
		StdErr: &bytes.Buffer{},
		JQ:     &iostreams.JQFilter{},
	}
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	var outputFormat string
	EnableJSONOutputP(cmd, ios, &outputFormat, "O")
	cmd.SetArgs([]string{"-O", "json"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, "O", cmd.Flag("output").Shorthand)
	assert.Equal(t, "json", outputFormat)
	assert.True(t, ios.IsJSONOutput())
	assert.NotNil(t, cmd.Flag("jq"))
}
