//go:build !integration

package iostreams

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
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

func TestNew_ColorMode(t *testing.T) {
	unsetColorEnv(t)

	tests := []struct {
		name      string
		mode      ColorMode
		wantColor bool
	}{
		{name: "unset defaults to auto detection", mode: "", wantColor: false},
		{name: "auto behaves like unset", mode: ColorModeAuto, wantColor: false},
		{name: "never forces color off", mode: ColorModeNever, wantColor: false},
		{name: "always forces color on regardless of TTY", mode: ColorModeAlways, wantColor: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", "dumb")
			t.Setenv("COLORTERM", "")

			opts := []IOStreamsOption{}
			if tt.mode != "" {
				opts = append(opts, WithColorMode(tt.mode))
			}
			ios := New(opts...)
			// New() without WithStdout/WithStderr leaves IsaTTY/IsErrTTY false,
			// so "auto"/unset resolve to no color here; only "always" must
			// override that to prove it forces color on.

			assert.Equal(t, tt.wantColor, ios.ColorEnabled())
		})
	}
}

// ColorProfile is the single value every output path derives from: whether
// color is on, and which tier it uses, are both read off it.
func TestIOStreams_ColorProfile(t *testing.T) {
	unsetColorEnv(t)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")

	t.Run("a non-TTY defaults to no color", func(t *testing.T) {
		ios := New()
		assert.Equal(t, colorprofile.NoTTY, ios.ColorProfile())
		assert.False(t, ios.ColorEnabled())
	})

	t.Run("always colors a pipe at the terminal tier", func(t *testing.T) {
		ios := New(WithColorMode(ColorModeAlways))
		assert.Equal(t, colorprofile.ANSI256, ios.ColorProfile())
		assert.True(t, ios.ColorEnabled())
	})

	t.Run("never wins on a TTY", func(t *testing.T) {
		ios := New(WithColorMode(ColorModeNever))
		ios.IsaTTY, ios.IsErrTTY = true, true
		ios.SetColorMode(ColorModeNever)
		assert.Equal(t, colorprofile.NoTTY, ios.ColorProfile())
		assert.False(t, ios.ColorEnabled())
	})

	t.Run("SetColorMode re-resolves the profile", func(t *testing.T) {
		ios := New()
		require.Equal(t, colorprofile.NoTTY, ios.ColorProfile())

		ios.SetColorMode(ColorModeAlways)
		assert.Equal(t, colorprofile.ANSI256, ios.ColorProfile())

		ios.SetColorMode(ColorModeNever)
		assert.Equal(t, colorprofile.NoTTY, ios.ColorProfile())
	})

	t.Run("WithColorEnabled pins the profile for tests", func(t *testing.T) {
		assert.Equal(t, colorprofile.ANSI256, New(WithColorEnabled(true)).ColorProfile())
		assert.Equal(t, colorprofile.NoTTY, New(WithColorEnabled(false), WithColorMode(ColorModeAlways)).ColorProfile())
	})
}

func TestIOStreams_Is256ColorSupported_IsATerminalCapability(t *testing.T) {
	unsetColorEnv(t)

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")
	assert.True(t, New().Is256ColorSupported(), "a 256-color terminal reports support even when color is off")

	t.Setenv("TERM", "xterm-16color")
	assert.False(t, New().Is256ColorSupported())
}

// Color-for-CI-logs colors a non-TTY, so it needs the terminal tier even
// though the stdout profile is NoTTY there.
func TestIOStreams_ColorForCILogs_KeepsTheTierOffATTY(t *testing.T) {
	unsetColorEnv(t)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")
	t.Setenv("GITLAB_CI", "true")

	ios := New()
	require.Equal(t, colorprofile.NoTTY, ios.ColorProfile())

	assert.Equal(t, "\x1b[38;5;242mtext\x1b[m", ios.ColorForCILogs().Gray("text"))
}

func TestIOStreams_SetColorMode(t *testing.T) {
	unsetColorEnv(t)
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "")

	ios := New()
	require.False(t, ios.ColorEnabled(), "precondition: no TTY, no explicit mode")

	ios.SetColorMode(ColorModeAlways)
	assert.True(t, ios.ColorEnabled())
	assert.Equal(t, ColorModeAlways, ios.ColorMode())

	ios.SetColorMode(ColorModeNever)
	assert.False(t, ios.ColorEnabled())
	assert.Equal(t, ColorModeNever, ios.ColorMode())
}

func TestIOStreams_ColorMode_DefaultsToAuto(t *testing.T) {
	ios := New()
	assert.Equal(t, ColorModeAuto, ios.ColorMode())
}

func unsetColorEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"NO_COLOR", "COLOR_ENABLED"} {
		if val, ok := os.LookupEnv(key); ok {
			os.Unsetenv(key)
			t.Cleanup(func() { t.Setenv(key, val) })
		}
	}
}

func TestResolveBackgroundColor(t *testing.T) {
	t.Run("explicit non-auto style is returned as-is", func(t *testing.T) {
		t.Setenv("GLAB_GLAMOUR_STYLE", "")
		t.Setenv("GLAMOUR_STYLE", "")
		ios := New(WithColorMode(ColorModeAlways))

		got := ios.ResolveBackgroundColor("notty")

		assert.Equal(t, "notty", got)
		assert.Equal(t, "notty", ios.BackgroundColor())
	})

	t.Run("GLAB_GLAMOUR_STYLE overrides a non-auto config style", func(t *testing.T) {
		t.Setenv("GLAB_GLAMOUR_STYLE", "light")
		t.Setenv("GLAMOUR_STYLE", "")
		ios := New(WithColorMode(ColorModeAlways))

		got := ios.ResolveBackgroundColor("dark")

		assert.Equal(t, "light", got)
	})

	t.Run("never beats an explicit style", func(t *testing.T) {
		t.Setenv("GLAB_GLAMOUR_STYLE", "")
		t.Setenv("GLAMOUR_STYLE", "")
		ios := New(WithColorMode(ColorModeNever))

		assert.Equal(t, "none", ios.ResolveBackgroundColor("dark"))
		assert.Equal(t, "none", ios.BackgroundColor())
	})

	t.Run("never beats a style from the environment", func(t *testing.T) {
		t.Setenv("GLAB_GLAMOUR_STYLE", "dark")
		t.Setenv("GLAMOUR_STYLE", "")
		ios := New(WithColorMode(ColorModeNever))

		assert.Equal(t, "none", ios.ResolveBackgroundColor("light"))
	})

	t.Run("auto with color disabled resolves to none", func(t *testing.T) {
		t.Setenv("GLAB_GLAMOUR_STYLE", "")
		t.Setenv("GLAMOUR_STYLE", "")
		ios := New(WithColorMode(ColorModeNever))

		got := ios.ResolveBackgroundColor("auto")

		assert.Equal(t, "none", got)
	})

	t.Run("auto with color enabled reaches real background detection", func(t *testing.T) {
		t.Setenv("GLAB_GLAMOUR_STYLE", "")
		t.Setenv("GLAMOUR_STYLE", "")
		ios := New(WithColorMode(ColorModeAlways))

		got := ios.ResolveBackgroundColor("auto")

		// Background detection depends on the environment, so only check that
		// "auto" resolves to a real value.
		assert.Contains(t, []string{"dark", "light"}, got)
	})
}
