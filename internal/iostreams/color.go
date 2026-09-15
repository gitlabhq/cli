package iostreams

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-colorable"
	"github.com/mgutz/ansi"
	"github.com/muesli/termenv"

	"gitlab.com/gitlab-org/cli/internal/theme"
)

type ColorPalette struct {
	// Magenta outputs ANSI color if stdout is a tty
	Magenta func(string) string
	// Cyan outputs ANSI color if stdout is a tty
	Cyan func(string) string
	// Red outputs ANSI color if stdout is a tty
	Red func(string) string
	// Yellow outputs ANSI color if stdout is a tty
	Yellow func(string) string
	// Blue outputs ANSI color if stdout is a tty
	Blue func(string) string
	// Green outputs ANSI color if stdout is a tty
	Green func(string) string
	// Gray outputs ANSI color if stdout is a tty
	Gray func(string) string
	// Bold outputs ANSI color if stdout is a tty
	Bold func(string) string
}

func (s *IOStreams) Color() *ColorPalette {
	return s.colorPalette(s.ColorEnabled() && s.IsaTTY)
}

// ColorForCILogs returns a palette that stays colored in GitLab CI job logs
// (which are not a TTY) while honoring NO_COLOR, unlike Color which gates on
// the TTY. Callers that need color in CI output should use this.
func (s *IOStreams) ColorForCILogs() *ColorPalette {
	return s.colorPalette(s.ColorEnabledForCILogs())
}

func (s *IOStreams) colorPalette(isColorfulOutput bool) *ColorPalette {
	var isDark bool
	switch s.BackgroundColor() { // could be simplified if commands like `ci list` called `ResolveBackgroundColor()`
	case "dark":
		isDark = true
	case "light":
		isDark = false
	default: // "none" — not yet resolved, detect now if color is enabled
		isDark = isColorfulOutput && termenv.HasDarkBackground()
	}
	lightDark := lipgloss.LightDark(isDark)
	glc := theme.NewGitLabColors(lightDark) // reuse existing palette

	return &ColorPalette{
		Magenta: makeColorFunc(isColorfulOutput, glc.Purple, "magenta"),
		Cyan:    makeColorFunc(isColorfulOutput, nil, "cyan"), // not in theme, falls back to ANSI
		Red:     makeColorFunc(isColorfulOutput, glc.Red, "red"),
		Yellow:  makeColorFunc(isColorfulOutput, nil, "yellow"), // not in theme, falls back to ANSI
		Blue:    makeColorFunc(isColorfulOutput, glc.Blue, "blue"),
		Green:   makeColorFunc(isColorfulOutput, glc.Green, "green"),
		Gray:    makeColorFunc(isColorfulOutput, nil, "black+h"),
		Bold:    makeColorFunc(isColorfulOutput, nil, "default+b"),
	}
}

// NewColorable returns an output stream that handles ANSI color sequences on Windows
func NewColorable(out io.Writer) io.Writer {
	if outFile, isFile := out.(*os.File); isFile {
		return colorable.NewColorable(outFile)
	}
	return out
}

func makeColorFunc(isColorfulOutput bool, brandColor color.Color, ansiName string) func(string) string {
	// don't bother doing terminal capacity checks and calculations if color is disabled
	if !isColorfulOutput {
		return func(arg string) string {
			return arg
		}
	}

	// 24-bit truecolor and we got a color from lipgloss'd theme
	if brandColor != nil && isTrueColorSupported() {
		r16, g16, b16, _ := brandColor.RGBA() // standard Go interface, 16-bit per channel
		r, g, b := uint8(r16>>8), uint8(g16>>8), uint8(b16>>8)
		return func(t string) string {
			return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[m", r, g, b, t)
		}
	}

	// 256 colors gray
	if ansiName == "black+h" && is256ColorSupported() {
		return func(t string) string {
			return fmt.Sprintf("\x1b[38;5;242m%s\x1b[m", t)
		}
	}

	// basic ANSI colors
	return ansi.ColorFunc(ansiName)
}

// detectIsColorEnabled determines whether color output should be enabled based on environment variables.
// It follows the NO_COLOR specification (https://no-color.org/) with an override mechanism:
//
// - If NO_COLOR environment variable exists (with any value), color is disabled by default
// - If COLOR_ENABLED is set to "1" or "true", it overrides NO_COLOR and forces color to be enabled
// - If NO_COLOR doesn't exist, color is enabled by default
//
// This allows users to disable color globally with NO_COLOR while still providing an escape hatch
// via COLOR_ENABLED for specific use cases.
// ColorEnabledForCILogs reports whether color should be emitted, treating a
// GitLab CI job log as color-capable even though it is not a TTY. It still
// honors the NO_COLOR contract (via detectIsColorEnabled): a CI job that sets
// NO_COLOR gets no color unless COLOR_ENABLED overrides it. Use this for output
// that is meant to stay colored in CI logs; everything else should keep using
// ColorEnabled/Color, which gate on the TTY.
func (s *IOStreams) ColorEnabledForCILogs() bool {
	if s.ColorEnabled() {
		return true
	}
	return detectIsColorEnabled() && inGitLabCI()
}

// inGitLabCI reports whether we're running inside a GitLab CI job.
func inGitLabCI() bool {
	return os.Getenv("GITLAB_CI") != ""
}

func detectIsColorEnabled() bool {
	// Check if NO_COLOR environment variable exists (any value disables color)
	_, noColorVarExists := os.LookupEnv("NO_COLOR")

	// If NO_COLOR exists, check if COLOR_ENABLED explicitly overrides it
	if noColorVarExists {
		colorEnabled := os.Getenv("COLOR_ENABLED")
		return colorEnabled == "1" || colorEnabled == "true"
	}

	// If NO_COLOR doesn't exist, color is enabled by default
	return true
}

func isTrueColorSupported() bool {
	term, colorterm := os.Getenv("TERM"), os.Getenv("COLORTERM")

	return strings.Contains(term, "24bit") || strings.Contains(term, "truecolor") ||
		strings.Contains(colorterm, "24bit") || strings.Contains(colorterm, "truecolor")
}

func is256ColorSupported() bool {
	term, colorterm := os.Getenv("TERM"), os.Getenv("COLORTERM")

	return strings.Contains(term, "256") || strings.Contains(colorterm, "256") ||
		isTrueColorSupported()
}
