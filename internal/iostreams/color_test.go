//go:build !integration

package iostreams

import (
	"image/color"
	"os"
	"testing"

	"charm.land/lipgloss/v2"
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
		colorField  bool // value of s.isColorEnabled (TTY-based)
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

			s := &IOStreams{isColorEnabled: tt.colorField}
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
		trueColor     color.Color
		colorEnabled  bool
		term          string
		want          string
	}{
		{
			name:          "gray 16 colors",
			ansiColorName: "black+h",
			trueColor:     nil,
			colorEnabled:  true,
			term:          "xterm-16color",
			want:          "\x1b[0;90mtext\x1b[0m",
		},
		{
			name:          "gray 256 colors",
			ansiColorName: "black+h",
			trueColor:     nil,
			colorEnabled:  true,
			term:          "xterm-256color",
			want:          "\x1b[38;5;242mtext\x1b[m",
		},
		{
			name:          "no colors",
			ansiColorName: "black+h",
			trueColor:     nil,
			colorEnabled:  false,
			term:          "",
			want:          "text",
		},
		{
			name:          "green when truecolor provided",
			ansiColorName: "green",
			trueColor:     lipgloss.Color("#34D058"),
			colorEnabled:  true,
			term:          "xterm-24bit",
			want:          "\x1b[38;2;52;208;88mtext\x1b[m",
		},
		{
			name:          "green when truecolor provided, but no terminal support",
			ansiColorName: "green",
			trueColor:     lipgloss.Color("#34D058"),
			colorEnabled:  true,
			term:          "xterm-256color",
			want:          "\x1b[0;32mtext\x1b[0m",
		},
		{
			name:          "green when no truecolor in palette",
			ansiColorName: "green",
			trueColor:     nil,
			colorEnabled:  true,
			term:          "xterm-24bit",
			want:          "\x1b[0;32mtext\x1b[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COLORTERM", "")
			t.Setenv("TERM", tt.term)

			fn := makeColorFunc(tt.colorEnabled, tt.trueColor, tt.ansiColorName)
			got := fn("text")

			require.Equal(t, tt.want, got)
		})
	}
}
