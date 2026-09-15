//go:build !integration

package summary

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// coloredPalette / plainPalette build a palette with color forced on or off
// via WithColorEnabled, so tests are deterministic and parallel-safe without
// touching ambient TTY/CI/NO_COLOR env.
func coloredPalette(t *testing.T) palette {
	t.Helper()
	buf := &strings.Builder{}
	ios := iostreams.New(
		iostreams.WithStdout(buf, true),
		iostreams.WithStderr(buf, true),
		iostreams.WithColorEnabled(true),
	)
	require.True(t, ios.ColorEnabled(), "WithColorEnabled(true) must force color on")
	return palette{c: ios.Color()}
}

func plainPalette(t *testing.T) palette {
	t.Helper()
	buf := &strings.Builder{}
	ios := iostreams.New(
		iostreams.WithStdout(buf, false),
		iostreams.WithStderr(buf, false),
		iostreams.WithColorEnabled(false),
	)
	require.False(t, ios.ColorEnabled(), "WithColorEnabled(false) must force color off")
	return palette{c: ios.Color()}
}

func mixedEntries() []verdict.Entry {
	return []verdict.Entry{
		{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"},
		{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Warning, Reason: "Deprecated package"},
	}
}

func TestRenderEmptyPrintsNoActivity(t *testing.T) {
	t.Parallel()
	ios, _, out, _ := cmdtest.TestIOStreams()
	Render(ios, nil)
	assert.Contains(t, out.String(), "no Dependency Firewall activity recorded")
}

func TestRenderIncludesTitleAndTableHeaders(t *testing.T) {
	t.Parallel()
	out := renderBody(plainPalette(t), mixedEntries(), 1, 1)
	assert.Contains(t, out, "GitLab Dependency Firewall")
	assert.Contains(t, out, "PACKAGE")
	assert.Contains(t, out, "VERSION")
	assert.Contains(t, out, "REASON")
}

func TestRenderMixedSummaryLine(t *testing.T) {
	t.Parallel()
	out := renderBody(plainPalette(t), mixedEntries(), 1, 1)
	assert.Contains(t, out, "1 blocked, 1 warning")
	assert.Contains(t, out, "lodash")
	assert.Contains(t, out, "left-pad")
}

func TestRenderSingleBlockedSummaryLine(t *testing.T) {
	t.Parallel()
	entries := []verdict.Entry{{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"}}
	out := renderBody(plainPalette(t), entries, 1, 0)
	assert.Contains(t, out, "1 package blocked")
}

func TestRenderSingleWarningSummaryLine(t *testing.T) {
	t.Parallel()
	entries := []verdict.Entry{{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Warning, Reason: "Deprecated package"}}
	out := renderBody(plainPalette(t), entries, 0, 1)
	assert.Contains(t, out, "1 package warning")
}

func TestRenderBlockedGoesToStderr(t *testing.T) {
	t.Parallel()
	ios, _, out, errOut := cmdtest.TestIOStreams()
	Render(ios, []verdict.Entry{{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"}})
	assert.Contains(t, errOut.String(), "GitLab Dependency Firewall", "blocked summary goes to stderr")
	assert.NotContains(t, out.String(), "GitLab Dependency Firewall")
}

func TestRenderWarningGoesToStdout(t *testing.T) {
	t.Parallel()
	ios, _, out, errOut := cmdtest.TestIOStreams()
	Render(ios, []verdict.Entry{{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Warning, Reason: "Deprecated package"}})
	assert.Contains(t, out.String(), "GitLab Dependency Firewall", "warning summary goes to stdout")
	assert.NotContains(t, errOut.String(), "GitLab Dependency Firewall")
}

func TestRenderStartsAndEndsWithBlankLine(t *testing.T) {
	t.Parallel()
	ios, _, _, errOut := cmdtest.TestIOStreams()
	Render(ios, []verdict.Entry{{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"}})
	got := errOut.String()
	assert.True(t, strings.HasPrefix(got, "\n"), "expected a leading blank line")
	assert.True(t, strings.HasSuffix(got, "\n\n"), "expected a trailing blank line")
}

func TestRenderLongPackageNameIsNotTruncated(t *testing.T) {
	t.Parallel()
	const longName = "@some-really-long-scope/a-package-with-a-long-name"
	entries := []verdict.Entry{{Package: longName, Version: "1.0.0", Verdict: verdict.Blocked, Reason: "policy violation"}}
	out := renderBody(plainPalette(t), entries, 1, 0)
	assert.Contains(t, out, longName, "long package name must render in full")
	assert.NotContains(t, out, "…")
	assert.NotContains(t, out, "...")
}

func TestRenderDrawsUnicodeBox(t *testing.T) {
	t.Parallel()
	out := renderBody(plainPalette(t), mixedEntries(), 1, 1)
	for _, ch := range []string{"╭", "╮", "╰", "╯", "│", "├", "┤", "─"} {
		assert.Contains(t, out, ch, "output should draw box char %q", ch)
	}
}

// TestRenderBoxIsAligned strips color and asserts every box line — borders AND
// content rows — has the same visible width, in both color states. This is the
// invariant that keeps the right border straight.
func TestRenderBoxIsAligned(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		p    palette
	}{
		{"plain", plainPalette(t)},
		{"colored", coloredPalette(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A crafted reason with control characters must not widen the box.
			entries := []verdict.Entry{
				{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy\tviolation\nsecond line"},
				{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Warning, Reason: "Deprecated package"},
			}
			out := renderBody(tc.p, entries, 1, 1)

			var width int
			seen := false
			for line := range strings.SplitSeq(strings.Trim(out, "\n"), "\n") {
				w := text.StringWidth(line)
				if !seen {
					width, seen = w, true
					continue
				}
				assert.Equal(t, width, w, "every box line must be the same visible width: %q", text.Strip(line))
			}
			assert.True(t, seen, "expected box lines")
		})
	}
}

func TestRenderColorStates(t *testing.T) {
	t.Parallel()

	t.Run("colored emits ANSI", func(t *testing.T) {
		t.Parallel()
		out := renderBody(coloredPalette(t), mixedEntries(), 1, 1)
		assert.Contains(t, out, "\x1b[", "colored palette should emit ANSI escapes")
	})

	t.Run("plain emits no ANSI", func(t *testing.T) {
		t.Parallel()
		out := renderBody(plainPalette(t), mixedEntries(), 1, 1)
		assert.NotContains(t, out, "\x1b[", "plain palette must not emit ANSI escapes")
	})
}

func TestRenderUsesStatusIcons(t *testing.T) {
	t.Parallel()
	out := renderBody(plainPalette(t), mixedEntries(), 1, 1)
	assert.Contains(t, out, blockedIcon)
	assert.Contains(t, out, warningIcon)
}

func TestRenderEmptyReasonShowsDash(t *testing.T) {
	t.Parallel()
	entries := []verdict.Entry{{Package: "express", Version: "4.18.2", Verdict: verdict.Allowed}}
	out := renderBody(plainPalette(t), entries, 0, 0)
	assert.Contains(t, out, emptyReason)
}

func TestSanitizeCell(t *testing.T) {
	t.Parallel()
	assert.Empty(t, sanitizeCell(""))
	assert.Equal(t, "a b c", sanitizeCell("a\tb\nc"))
	assert.Equal(t, "malware", sanitizeCell("\x1b[31mmalware\x1b[0m"), "ANSI escapes must be stripped")
	assert.NotContains(t, sanitizeCell("x\x1b[2Jy"), "\x1b", "control sequences removed")
}

func TestReasonOrDash(t *testing.T) {
	t.Parallel()
	assert.Equal(t, emptyReason, reasonOrDash(""))
	assert.Equal(t, emptyReason, reasonOrDash("  \t "), "whitespace-only reason falls back to dash")
	assert.Equal(t, "policy violation", reasonOrDash("policy violation"))
}

// TestRenderSanitizesPackageAndVersion pins that a crafted package name or
// version can't inject control characters or tear the box.
func TestRenderSanitizesPackageAndVersion(t *testing.T) {
	t.Parallel()
	entries := []verdict.Entry{
		{Package: "evil\x1b[2Jpkg", Version: "1.0\n0", Verdict: verdict.Blocked, Reason: "policy violation"},
	}
	out := renderBody(plainPalette(t), entries, 1, 0)
	assert.NotContains(t, out, "\x1b", "package/version escapes must be stripped")
	assert.NotContains(t, out, "\n1.0\n0", "version newline must be collapsed")
	assert.Contains(t, out, "evilpkg")
}

func TestWorstVerdict(t *testing.T) {
	t.Parallel()
	assert.Equal(t, verdict.Blocked, worstVerdict(2, 1))
	assert.Equal(t, verdict.Blocked, worstVerdict(1, 0))
	assert.Equal(t, verdict.Warning, worstVerdict(0, 3))
	assert.Equal(t, verdict.Allowed, worstVerdict(0, 0))
}

func TestIconForMatchesPaletteIcon(t *testing.T) {
	t.Parallel()
	p := plainPalette(t)
	for _, v := range []verdict.Verdict{verdict.Blocked, verdict.Warning, verdict.Allowed} {
		assert.Equal(t, iconFor(v), p.icon(v), "plain palette icon must equal the plain glyph for %q", v)
	}
}

func TestAccentByWorstVerdict(t *testing.T) {
	t.Parallel()
	c := coloredPalette(t)
	blocked := c.accent(verdict.Blocked)("x")
	warned := c.accent(verdict.Warning)("x")
	allowed := c.accent(verdict.Allowed)("x")
	// Each severity produces a distinct colorization.
	assert.NotEqual(t, blocked, warned)
	assert.NotEqual(t, warned, allowed)
	assert.NotEqual(t, blocked, allowed)
}

// TestNewPaletteSourcesColorForCILogs pins that newPalette derives its color
// from ColorForCILogs, not Color: with color forced on but the streams marked
// non-TTY (as in a CI job log), the summary is still colored. The CI/NO_COLOR
// decision itself is covered by TestColorEnabledForCILogs in the iostreams
// package, so this test needs no env mutation.
func TestNewPaletteSourcesColorForCILogs(t *testing.T) {
	t.Parallel()
	buf := &strings.Builder{}
	ios := iostreams.New(
		iostreams.WithStdout(buf, false),
		iostreams.WithStderr(buf, false),
		iostreams.WithColorEnabled(true),
	)
	require.False(t, ios.IsaTTY, "streams must be non-TTY to prove color isn't gated on the TTY")
	out := renderBody(newPalette(ios), mixedEntries(), 1, 1)
	assert.Contains(t, out, "\x1b[", "summary must stay colored for CI logs even off a TTY")
}
