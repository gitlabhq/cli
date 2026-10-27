//go:build !integration

package iostreams

import (
	"image/color"
	"os"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_isColorEnabled(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		for _, key := range []string{"NO_COLOR", "COLOR_ENABLED"} {
			if val, ok := os.LookupEnv(key); ok {
				os.Unsetenv(key)
				t.Cleanup(func() { t.Setenv(key, val) })
			}
		}

		got := detectIsColorEnabled()
		assert.True(t, got)
	})

	t.Run("NO_COLOR", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")

		got := detectIsColorEnabled()
		assert.False(t, got)
	})

	t.Run("COLOR_ENABLED == 1", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("COLOR_ENABLED", "1")

		got := detectIsColorEnabled()
		assert.True(t, got)
	})

	t.Run("COLOR_ENABLED == true", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("COLOR_ENABLED", "true")

		got := detectIsColorEnabled()
		assert.True(t, got)
	})
}

func TestColorEnabledForCILogs(t *testing.T) {
	tests := []struct {
		name        string
		colorField  bool // whether the stdout profile has color (TTY-based)
		mode        ColorMode
		inCI        bool
		setNoColor  bool
		colorEnable string // COLOR_ENABLED value; "" means unset
		want        bool
	}{
		{name: "TTY color already enabled", colorField: true, want: true},
		{name: "not a TTY and not CI", colorField: false, want: false},
		{name: "not a TTY but in GitLab CI", colorField: false, inCI: true, want: true},
		{name: "in CI but NO_COLOR set", colorField: false, inCI: true, setNoColor: true, want: false},
		{name: "in CI, NO_COLOR set, COLOR_ENABLED override", colorField: false, inCI: true, setNoColor: true, colorEnable: "1", want: true},
		{name: "in CI but color is never", colorField: false, mode: ColorModeNever, inCI: true, want: false},
		{name: "never wins over COLOR_ENABLED in CI", colorField: false, mode: ColorModeNever, inCI: true, setNoColor: true, colorEnable: "1", want: false},
		{name: "in CI with color always", colorField: true, mode: ColorModeAlways, inCI: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Start from a clean slate: unset the vars this decision reads so
			// a case that doesn't set one isn't affected by ambient values.
			// The cases below set what they need via t.Setenv, which already
			// registers its own restore cleanup.
			unsetEnvForTest(t, "NO_COLOR", "COLOR_ENABLED", "GITLAB_CI")
			if tt.inCI {
				t.Setenv("GITLAB_CI", "true")
			}
			if tt.setNoColor {
				t.Setenv("NO_COLOR", "")
			}
			if tt.colorEnable != "" {
				t.Setenv("COLOR_ENABLED", tt.colorEnable)
			}

			s := &IOStreams{colorMode: tt.mode, colorProfile: profileIf(tt.colorField)}
			assert.Equal(t, tt.want, s.ColorEnabledForCILogs())
		})
	}
}

// unsetEnvForTest unsets env vars for the duration of the test and restores
// their original values on cleanup. The testing package has no t.Unsetenv, and
// t.Setenv can only set (not remove) a var, so the restore uses os.Setenv.
func unsetEnvForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if val, ok := os.LookupEnv(key); ok {
			os.Unsetenv(key)
			t.Cleanup(func() { os.Setenv(key, val) }) //nolint:usetesting // restoring an unset var; t.Setenv can't remove/restore-absent
		}
	}
}

func Test_makeColorFunc(t *testing.T) {
	tests := []struct {
		name          string
		ansiColorName string
		brandColor    color.Color
		profile       colorprofile.Profile
		want          string
	}{
		{name: "gray 16 colors", ansiColorName: "black+h", profile: colorprofile.ANSI, want: "\x1b[0;90mtext\x1b[0m"},
		{name: "gray 256 colors", ansiColorName: "black+h", profile: colorprofile.ANSI256, want: "\x1b[38;5;242mtext\x1b[m"},
		{name: "gray under truecolor uses the 256-color gray", ansiColorName: "black+h", profile: colorprofile.TrueColor, want: "\x1b[38;5;242mtext\x1b[m"},
		{name: "no colors", ansiColorName: "black+h", profile: colorprofile.NoTTY, want: "text"},
		{name: "ascii profile has no colors", ansiColorName: "green", brandColor: lipgloss.Color("#34D058"), profile: colorprofile.ASCII, want: "text"},
		{name: "green when truecolor provided", ansiColorName: "green", brandColor: lipgloss.Color("#34D058"), profile: colorprofile.TrueColor, want: "\x1b[38;2;52;208;88mtext\x1b[m"},
		{name: "green when truecolor provided, but no terminal support", ansiColorName: "green", brandColor: lipgloss.Color("#34D058"), profile: colorprofile.ANSI256, want: "\x1b[0;32mtext\x1b[0m"},
		{name: "green when no truecolor in palette", ansiColorName: "green", profile: colorprofile.TrueColor, want: "\x1b[0;32mtext\x1b[0m"},
		{name: "magenta 16 colors", ansiColorName: "magenta", profile: colorprofile.ANSI, want: "\x1b[0;35mtext\x1b[0m"},
		{name: "cyan 16 colors", ansiColorName: "cyan", profile: colorprofile.ANSI, want: "\x1b[0;36mtext\x1b[0m"},
		{name: "red 16 colors", ansiColorName: "red", profile: colorprofile.ANSI, want: "\x1b[0;31mtext\x1b[0m"},
		{name: "yellow 16 colors", ansiColorName: "yellow", profile: colorprofile.ANSI, want: "\x1b[0;33mtext\x1b[0m"},
		{name: "blue 16 colors", ansiColorName: "blue", profile: colorprofile.ANSI, want: "\x1b[0;34mtext\x1b[0m"},
		{name: "default+b 16 colors", ansiColorName: "default+b", profile: colorprofile.ANSI, want: "\x1b[0;1;39mtext\x1b[0m"},
		{name: "unknown name is left uncolored", ansiColorName: "nope", profile: colorprofile.ANSI, want: "text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, makeColorFunc(tt.profile, tt.brandColor, tt.ansiColorName)("text"))
		})
	}
}

func Test_colorTier(t *testing.T) {
	tests := []struct {
		name      string
		term      string
		colorterm string
		want      colorprofile.Profile
	}{
		{name: "24bit TERM", term: "xterm-24bit", want: colorprofile.TrueColor},
		{name: "truecolor TERM", term: "xterm-truecolor", want: colorprofile.TrueColor},
		{name: "truecolor COLORTERM", term: "xterm", colorterm: "truecolor", want: colorprofile.TrueColor},
		{name: "24bit COLORTERM", term: "xterm", colorterm: "24bit", want: colorprofile.TrueColor},
		{name: "256color TERM", term: "xterm-256color", want: colorprofile.ANSI256},
		{name: "256 COLORTERM", term: "xterm", colorterm: "256", want: colorprofile.ANSI256},
		{name: "16 colors", term: "xterm-16color", want: colorprofile.ANSI},
		{name: "dumb terminal still has a tier for forced color", term: "dumb", want: colorprofile.ANSI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", tt.term)
			t.Setenv("COLORTERM", tt.colorterm)

			require.Equal(t, tt.want, colorTier())
		})
	}
}

func Test_resolveColorProfile(t *testing.T) {
	tests := []struct {
		name     string
		mode     ColorMode
		isTTY    bool
		noColor  string
		colorEnv string
		want     colorprofile.Profile
	}{
		{name: "auto on a TTY takes the terminal tier", mode: ColorModeAuto, isTTY: true, want: colorprofile.ANSI256},
		{name: "unset mode behaves like auto", mode: "", isTTY: true, want: colorprofile.ANSI256},
		{name: "auto off a TTY is NoTTY", mode: ColorModeAuto, isTTY: false, want: colorprofile.NoTTY},
		{name: "auto honors NO_COLOR", mode: ColorModeAuto, isTTY: true, noColor: "1", want: colorprofile.NoTTY},
		{name: "COLOR_ENABLED overrides NO_COLOR under auto", mode: ColorModeAuto, isTTY: true, noColor: "1", colorEnv: "true", want: colorprofile.ANSI256},
		{name: "never is NoTTY even on a TTY", mode: ColorModeNever, isTTY: true, want: colorprofile.NoTTY},
		{name: "always colors a non-TTY", mode: ColorModeAlways, isTTY: false, want: colorprofile.ANSI256},
		{name: "always beats NO_COLOR", mode: ColorModeAlways, isTTY: false, noColor: "1", want: colorprofile.ANSI256},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("COLORTERM", "")
			unsetColorEnv(t)
			if tt.noColor != "" {
				t.Setenv("NO_COLOR", tt.noColor)
			}
			if tt.colorEnv != "" {
				t.Setenv("COLOR_ENABLED", tt.colorEnv)
			}

			require.Equal(t, tt.want, resolveColorProfile(tt.mode, tt.isTTY))
		})
	}
}

func TestColorMode_AllColorModes(t *testing.T) {
	got := AllColorModes()
	require.Equal(t, []ColorMode{
		ColorModeAuto,
		ColorModeNever,
		ColorModeAlways,
	}, got)
}

func Test_makeColorFunc_emptyText(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM", "xterm-16color")

	require.Empty(t, makeColorFunc(colorprofile.ANSI, nil, "green")(""))
}
