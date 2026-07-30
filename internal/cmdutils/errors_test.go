package cmdutils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// errorHandlerHarness returns IOStreams with a capturable stdout, the stdout
// buffer itself, and fangOut — the writer fang hands the error handler
// directly for the human-readable box, standing in for fang's own
// colorprofile-wrapped stderr in the real binary.
func errorHandlerHarness(t *testing.T) (*iostreams.IOStreams, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	fangOut := &bytes.Buffer{}
	ios := &iostreams.IOStreams{StdOut: stdout, JQ: &iostreams.JQFilter{}}
	return ios, stdout, fangOut
}

// jsonErrorOf decodes the machine-readable error object written to stdout.
func jsonErrorOf(t *testing.T, stdout *bytes.Buffer) string {
	t.Helper()
	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	return got.Error.Message
}

func TestNewGitLabErrorHandler_TextOutput_StdoutStaysEmpty(t *testing.T) {
	ios, stdout, fangOut := errorHandlerHarness(t)

	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, errors.New("404 Not Found"))

	assert.Empty(t, stdout.String(), "text output must not put anything on stdout")
	assert.Contains(t, fangOut.String(), "404 Not Found")
}

func TestNewGitLabErrorHandler_JSONOutput_WritesObjectToStdout(t *testing.T) {
	ios, stdout, fangOut := errorHandlerHarness(t)
	ios.SetOutputFormat("json")

	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, errors.New("404 Not Found"))

	assert.Equal(t, "404 Not Found", jsonErrorOf(t, stdout))
	assert.Contains(t, fangOut.String(), "404 Not Found", "human-readable error still goes to stderr")
}

// ExitError.Error() returns only the wrapped error's message; the "log"
// string passed to WrapError is not part of it. This pins that the JSON
// envelope follows the same contract rather than trying to surface the log
// text too.
func TestNewGitLabErrorHandler_JSONOutput_ExitErrorOmitsLogDetail(t *testing.T) {
	ios, stdout, fangOut := errorHandlerHarness(t)
	ios.SetOutputFormat("json")

	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, WrapError(errors.New("403 Forbidden"), "cannot read variable"))

	assert.Equal(t, "403 Forbidden", jsonErrorOf(t, stdout))
}

func TestNewGitLabErrorHandler_JSONOutput_ContextCanceled(t *testing.T) {
	ios, stdout, fangOut := errorHandlerHarness(t)
	ios.SetOutputFormat("json")

	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, fmt.Errorf("aborted: %w", context.Canceled))

	assert.Equal(t, errCommandInterrupted.Error(), jsonErrorOf(t, stdout))
	assert.Contains(t, fangOut.String(), errCommandInterrupted.Error())
}

func TestNewGitLabErrorHandler_TextOutput_ContextCanceled(t *testing.T) {
	ios, stdout, fangOut := errorHandlerHarness(t)

	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, fmt.Errorf("aborted: %w", context.Canceled))

	assert.Empty(t, stdout.String())
	assert.Contains(t, fangOut.String(), errCommandInterrupted.Error())
}

func TestNewGitLabErrorHandler_SilentError_WritesNothing(t *testing.T) {
	ios, stdout, fangOut := errorHandlerHarness(t)

	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, SilentError)
	assert.Empty(t, stdout.String())
	assert.Empty(t, fangOut.String())

	ios.SetOutputFormat("json")
	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, SilentError)
	assert.Empty(t, stdout.String(), "SilentError must stay silent in JSON output too")
	assert.Empty(t, fangOut.String())
}

func TestNewGitLabErrorHandler_JSONOutput_IgnoresJQFilter(t *testing.T) {
	// --jq is written against the command's success output, so applying it to
	// the error object would hide the failure behind a filter error.
	ios, stdout, fangOut := errorHandlerHarness(t)
	ios.SetOutputFormat("json")
	require.NoError(t, ios.JQ.Set(".[].id"))

	NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, errors.New("404 Not Found"))

	assert.Equal(t, "404 Not Found", jsonErrorOf(t, stdout))
}

// TestGitLabErrorHandler_EndToEnd drives the whole path the issue describes: a
// command registers --output through EnableJSONOutput, fails before printing
// anything, and fang hands the resulting error to our handler.
func TestGitLabErrorHandler_EndToEnd(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantJSONStdout bool
		wantMessage    string
	}{
		{
			name:           "failure inside RunE with --output=json",
			args:           []string{"KEY", "--output", "json"},
			wantJSONStdout: true,
			wantMessage:    "404 Not Found",
		},
		{
			name:           "failure during argument validation with -F json",
			args:           []string{"-F", "json"},
			wantJSONStdout: true,
			wantMessage:    "accepts 1 arg(s), received 0",
		},
		{
			name:           "failure inside RunE without an output flag",
			args:           []string{"KEY"},
			wantJSONStdout: false,
			wantMessage:    "404 Not Found",
		},
		{
			name:           "failure inside RunE with an explicit text output flag",
			args:           []string{"KEY", "-F", "text"},
			wantJSONStdout: false,
			wantMessage:    "404 Not Found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ios, stdout, fangOut := errorHandlerHarness(t)
			cmd := &cobra.Command{
				Use:           "test <key>",
				Args:          cobra.ExactArgs(1),
				SilenceUsage:  true,
				SilenceErrors: true,
				RunE:          func(*cobra.Command, []string) error { return errors.New("404 Not Found") },
			}
			var outputFormat string
			EnableJSONOutput(cmd, ios, &outputFormat)
			cmd.SetArgs(tc.args)

			err := cmd.Execute()
			require.Error(t, err)

			NewGitLabErrorHandler(ios)(fangOut, fang.Styles{}, err)

			assert.Contains(t, fangOut.String(), tc.wantMessage)
			if !tc.wantJSONStdout {
				assert.Empty(t, stdout.String())
				return
			}
			assert.Equal(t, tc.wantMessage, jsonErrorOf(t, stdout))
		})
	}
}
