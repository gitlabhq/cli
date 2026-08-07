package cmdutils

import (
	"context"
	"errors"
	"fmt"
	"io"

	"charm.land/fang/v2"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// FlagError is the kind of error raised in flag processing
type FlagError struct {
	Err error
}

func (fe FlagError) Error() string {
	return fe.Err.Error()
}

func (fe FlagError) Unwrap() error {
	return fe.Err
}

var (
	// SilentError is an error that triggers exit Code 1 without any error messaging
	SilentError = errors.New("SilentError")

	errCommandInterrupted = errors.New("the command execution has been interrupted")
)

// NewGitLabErrorHandler returns a custom error handler for fang that handles
// GitLab CLI specific errors. Under --output=json the failure is also written
// to stdout as an object, so a caller parsing glab's output is not left with an
// empty stream; the human-readable error still goes to stderr in every mode.
//
// fang passes only the error, never the *cobra.Command, so the format is read
// back from streams. streams must not be nil.
func NewGitLabErrorHandler(streams *iostreams.IOStreams) fang.ErrorHandler {
	return func(w io.Writer, styles fang.Styles, err error) {
		switch {
		case errors.Is(err, context.Canceled):
			err = errCommandInterrupted
		case errors.Is(err, SilentError):
			// Ignore SilentError - it should not produce any output
			return
		}

		if streams.IsJSONOutput() {
			if printErr := streams.PrintJSONError(err); printErr != nil {
				err = errors.Join(err, printErr)
			}
		}

		// Delegate the human-readable rendering to Fang's default handler
		fang.DefaultErrorHandler(w, styles, err)
	}
}

type ExitError struct {
	Err     error
	Code    int
	Details string
}

func WrapErrorWithCode(err error, code int, details string) *ExitError {
	return &ExitError{
		Err:     err,
		Code:    code,
		Details: details,
	}
}

func WrapError(err error, log string) *ExitError {
	return WrapErrorWithCode(err, 1, log)
}

func CancelError(log ...any) error {
	if len(log) < 1 {
		return WrapErrorWithCode(iostreams.ErrUserCancelled, 2, "action cancelled")
	}
	return WrapErrorWithCode(iostreams.ErrUserCancelled, 2, fmt.Sprint(log...))
}

func (e *ExitError) Error() string {
	return e.Err.Error()
}

func (e ExitError) Unwrap() error {
	return e.Err
}
