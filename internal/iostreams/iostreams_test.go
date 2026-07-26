//go:build !integration

package iostreams

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_HelperFunctions(t *testing.T) {
	// Base ios object that is modified as required
	ios := &IOStreams{
		In:     os.Stdin,
		StdOut: NewColorable(os.Stdout),
		StdErr: NewColorable(os.Stderr),

		IsaTTY:         IsTerminal(os.Stdout),
		IsErrTTY:       IsTerminal(os.Stderr),
		IsInTTY:        IsTerminal(os.Stdin),
		promptDisabled: false,

		pagerCommand: os.Getenv("PAGER"),
	}

	t.Run("InitIOStream()", func(t *testing.T) {
		t.Run("PAGER=", func(t *testing.T) {
			t.Setenv("PAGER", "")

			got := New(
				WithStdin(os.Stdin, IsTerminal(os.Stdin)),
				WithStdout(NewColorable(os.Stdout), IsTerminal(os.Stdout)),
				WithStderr(NewColorable(os.Stderr), IsTerminal(os.Stderr)),
				WithPagerCommand(PagerCommandFromEnv()),
			)

			assert.Equal(t, ios.In, got.In)
			assert.Equal(t, ios.IsaTTY, got.IsaTTY)
			assert.Equal(t, ios.IsErrTTY, got.IsErrTTY)
			assert.Equal(t, ios.IsInTTY, got.IsInTTY)
			assert.Equal(t, ios.promptDisabled, got.promptDisabled)
			assert.Empty(t, got.pagerCommand)
		})
		t.Run("GLAB_PAGER=", func(t *testing.T) {
			t.Setenv("GLAB_PAGER", "more")

			got := New(
				WithStdin(os.Stdin, IsTerminal(os.Stdin)),
				WithStdout(NewColorable(os.Stdout), IsTerminal(os.Stdout)),
				WithStderr(NewColorable(os.Stderr), IsTerminal(os.Stderr)),
				WithPagerCommand(PagerCommandFromEnv()),
			)

			assert.Equal(t, ios.In, got.In)
			assert.Equal(t, ios.IsaTTY, got.IsaTTY)
			assert.Equal(t, ios.IsErrTTY, got.IsErrTTY)
			assert.Equal(t, ios.IsInTTY, got.IsInTTY)
			assert.Equal(t, ios.promptDisabled, got.promptDisabled)
			assert.Equal(t, "more", got.pagerCommand)
		})
	})

	t.Run("IsOutputTTY()", func(t *testing.T) {
		t.Run("true", func(t *testing.T) {
			ios := *ios

			ios.IsaTTY = true
			ios.IsErrTTY = true

			got := ios.IsOutputTTY()
			assert.True(t, got)
		})
		t.Run("false", func(t *testing.T) {
			t.Run("IsaTTY=false", func(t *testing.T) {
				ios := *ios

				ios.IsaTTY = false
				ios.IsErrTTY = true

				got := ios.IsOutputTTY()
				assert.False(t, got)
			})
			t.Run("IsErrTTY=false", func(t *testing.T) {
				ios := *ios

				ios.IsaTTY = true
				ios.IsErrTTY = false

				got := ios.IsOutputTTY()
				assert.False(t, got)
			})
		})
	})

	t.Run("SetPager()", func(t *testing.T) {
		t.Run("more", func(t *testing.T) {
			ios := *ios
			ios.SetPager("more")
			assert.Equal(t, "more", ios.pagerCommand)
		})
	})

	t.Run("PromptEnabled()", func(t *testing.T) {
		t.Run("true", func(t *testing.T) {
			var got bool
			ios := *ios

			ios.promptDisabled = false
			ios.IsaTTY = true
			ios.IsErrTTY = true

			got = ios.PromptEnabled()
			assert.True(t, got)
		})

		t.Run("false", func(t *testing.T) {
			t.Run("promptDisabled=true", func(t *testing.T) {
				var got bool
				ios := *ios

				ios.promptDisabled = true
				got = ios.PromptEnabled()
				assert.False(t, got)
			})

			t.Run("IsaTTY=false", func(t *testing.T) {
				var got bool
				ios := *ios

				ios.IsaTTY = false
				got = ios.PromptEnabled()
				assert.False(t, got)
			})

			t.Run("IsErrTTY=true", func(t *testing.T) {
				var got bool
				ios := *ios

				ios.IsErrTTY = false
				got = ios.PromptEnabled()
				assert.False(t, got)
			})
		})
	})

	t.Run("SetPrompt()", func(t *testing.T) {
		t.Run("disabled", func(t *testing.T) {
			t.Run("true", func(t *testing.T) {
				ios := *ios
				ios.SetPrompt("true")
				assert.True(t, ios.promptDisabled)
			})
			t.Run("1", func(t *testing.T) {
				ios := *ios
				ios.SetPrompt("1")
				assert.True(t, ios.promptDisabled)
			})
		})
		t.Run("enabled", func(t *testing.T) {
			t.Run("false", func(t *testing.T) {
				ios := *ios
				ios.SetPrompt("false")
				assert.False(t, ios.promptDisabled)
			})
			t.Run("0", func(t *testing.T) {
				ios := *ios
				ios.SetPrompt("0")
				assert.False(t, ios.promptDisabled)
			})
		})
	})
}

func Test_stripControlCharacters(t *testing.T) {
	type args struct {
		badString string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "With moving 2 lines up",
			args: args{
				badString: "echo evil!" + //
					"exit 0" + //
					"[2Aecho Hello World!",
			},
			want: "echo evil!exit 0^[[2Aecho Hello World!",
		},
		{
			name: "With obfuscating characters",
			args: args{
				badString: "echo evil!" + //
					"exit 0" + //
					"[2;0;0;5;3;2;Aecho Hello World!",
			},
			want: "echo evil!exit 0^[[2;0;0;5;3;2;Aecho Hello World!",
		},
		{
			name: "With clearing the screen",
			args: args{
				badString: "echo evil!" + //
					"exit 0" + //
					"[2Lecho Hello World!",
			},
			want: "echo evil!exit 0^[[2Lecho Hello World!",
		},
		{
			name: "control character with empty parameters",
			args: args{
				badString: "[2;;;Aecho Hello World!",
			},
			want: "^[[2;;;Aecho Hello World!",
		},
		{
			name: "With colors",
			args: args{
				badString: "\033[0;30mSome text",
			},
			want: "\x1b[0;30mSome text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripControlCharacters(tt.args.badString)
			assert.Equal(t, tt.want, got)
		})
	}
}

// A PAGER holding only whitespace is not a runnable command. shlex.Split
// returns no arguments for it, so building the command indexed into an empty
// slice and panicked. Treat it the same as an unset pager.
func TestStartPager_BlankCommand(t *testing.T) {
	for _, pager := range []string{"   ", "\t", "\n", " \t\n "} {
		t.Run(fmt.Sprintf("%q", pager), func(t *testing.T) {
			ios := New(WithPagerCommand(pager))
			ios.IsaTTY = true
			originalStdOut := ios.StdOut

			err := ios.StartPager()

			require.NoError(t, err)
			assert.Equal(t, originalStdOut, ios.StdOut, "no pager should have been started")
		})
	}
}

func TestWritePagerOutput_LongLine(t *testing.T) {
	longLine := strings.Repeat("x", 70*1024)
	var output bytes.Buffer

	err := writePagerOutput(&output, strings.NewReader(longLine))

	require.NoError(t, err)
	assert.Equal(t, longLine+"\n", output.String())
}
