package summary

import (
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/text"
)

const title = "GitLab Dependency Firewall"

// Status icons. Kept as constants so the rendered glyphs and the test
// assertions reference the same source.
const (
	blockedIcon = "✖"
	warningIcon = "▲"
	allowedIcon = "✔"
	emptyReason = "—"
)

// Render writes a summary of Dependency Firewall verdicts inside a rounded
// Unicode box tinted by the worst verdict in the run. Blocked runs go to
// stderr, warning/allow-only runs go to stdout. A blank line is emitted both
// before and after the box so it stands apart from surrounding wrapper or CI
// output.
func Render(io *iostreams.IOStreams, entries []verdict.Entry) {
	if len(entries) == 0 {
		io.LogInfo("no Dependency Firewall activity recorded")
		return
	}

	var blocked, warned int
	for _, e := range entries {
		switch e.Verdict {
		case verdict.Blocked:
			blocked++
		case verdict.Warning:
			warned++
		}
	}

	body := renderBody(newPalette(io), entries, blocked, warned)
	if blocked > 0 {
		io.LogError(body)
	} else {
		io.LogInfo(body)
	}
}

// renderBody builds the boxed summary. The palette is injected so tests can
// exercise the colored and plain layouts deterministically, without depending
// on ambient color/TTY/CI environment.
func renderBody(p palette, entries []verdict.Entry, blocked, warned int) string {
	worst := worstVerdict(blocked, warned)
	accent := p.accent(worst)

	// Sanitize every cell before it touches the layout or the terminal.
	// Package and version are derived from network/PURL input and the reason
	// is a server response, so all three are untrusted: stripping escapes and
	// collapsing whitespace stops a crafted value from tearing the box or
	// emitting control characters.
	type cells struct {
		verdict       verdict.Verdict
		pkg, ver, rsn string
	}
	rows := make([]cells, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, cells{
			verdict: e.Verdict,
			pkg:     sanitizeCell(e.Package),
			ver:     sanitizeCell(e.Version),
			rsn:     reasonOrDash(e.Reason),
		})
	}

	// Column widths measured with text.StringWidth so multi-cell glyphs and
	// any ANSI escapes are accounted for.
	wPkg, wVer := text.StringWidth("PACKAGE"), text.StringWidth("VERSION")
	for _, r := range rows {
		wPkg = max(wPkg, text.StringWidth(r.pkg))
		wVer = max(wVer, text.StringWidth(r.ver))
	}

	// One row-formatter builds every content line so the icon column, the
	// column widths, and the two-space gutters are defined in a single place
	// (rather than repeated across format strings that could drift).
	row := func(icon, pkg, ver, reason string) string {
		return fmt.Sprintf("%s  %s  %s  %s",
			icon, text.PadRight(pkg, wPkg, ' '), text.PadLeft(ver, wVer, ' '), reason)
	}

	header := row(" ", p.c.Bold("PACKAGE"), p.c.Bold("VERSION"), p.c.Bold("REASON"))

	bodyRows := make([]string, 0, len(rows))
	for _, r := range rows {
		bodyRows = append(bodyRows, row(p.icon(r.verdict), r.pkg, r.ver, p.c.Gray(r.rsn)))
	}

	titleLine := accent(iconFor(worst)) + "  " + p.c.Bold(title)
	summaryLine := p.summary(len(entries), blocked, warned)

	// inner is the widest content line; every line is left-margined and
	// right-padded to it by box() below, so alignment is structural.
	inner := text.StringWidth(header)
	for _, l := range bodyRows {
		inner = max(inner, text.StringWidth(l))
	}
	inner = max(inner, text.StringWidth(titleLine))
	inner = max(inner, text.StringWidth(summaryLine))

	var b strings.Builder
	box := newBoxWriter(&b, accent, inner)
	b.WriteByte('\n')
	box.top()
	box.row(titleLine)
	box.row(summaryLine)
	box.divider()
	box.row(header)
	for _, l := range bodyRows {
		box.row(l)
	}
	box.bottom()
	return b.String()
}

// boxWriter draws a rounded box of a fixed interior width. It owns the left
// margin (one space) and right padding for every content row, so callers pass
// only the content and can't drift the alignment.
type boxWriter struct {
	b      *strings.Builder
	accent func(string) string
	inner  int
}

func newBoxWriter(b *strings.Builder, accent func(string) string, inner int) boxWriter {
	// +2 for the one-space margin on each side of the content.
	return boxWriter{b: b, accent: accent, inner: inner + 2}
}

func (w boxWriter) top() {
	w.b.WriteString(w.accent("╭"+strings.Repeat("─", w.inner)+"╮") + "\n")
}

func (w boxWriter) divider() {
	w.b.WriteString(w.accent("├"+strings.Repeat("─", w.inner)+"┤") + "\n")
}

func (w boxWriter) bottom() {
	w.b.WriteString(w.accent("╰"+strings.Repeat("─", w.inner)+"╯") + "\n")
}

func (w boxWriter) row(content string) {
	// One leading space, content padded to the interior width, one trailing
	// space, wrapped in borders.
	padded := " " + content + strings.Repeat(" ", w.inner-1-text.StringWidth(content))
	w.b.WriteString(w.accent("│") + padded + w.accent("│") + "\n")
}

// iconFor returns the plain status glyph for a verdict. It is the single source
// of the verdict→glyph mapping; palette.icon colors this result.
func iconFor(v verdict.Verdict) string {
	switch v {
	case verdict.Blocked:
		return blockedIcon
	case verdict.Warning:
		return warningIcon
	default:
		return allowedIcon
	}
}

// worstVerdict returns the most severe verdict present, so the title icon and
// the box accent both key off the same severity.
func worstVerdict(blocked, warned int) verdict.Verdict {
	switch {
	case blocked > 0:
		return verdict.Blocked
	case warned > 0:
		return verdict.Warning
	default:
		return verdict.Allowed
	}
}

// sanitizeCell makes an untrusted cell value safe to render inside the box: it
// strips ANSI/OSC escapes and collapses any whitespace (including tabs and
// newlines) to single spaces, so a crafted value can't tear the box or write
// control characters to the terminal.
func sanitizeCell(s string) string {
	return strings.Join(strings.Fields(text.Strip(s)), " ")
}

// reasonOrDash sanitizes a reason and falls back to a dash when it is empty.
// Blocked and warned entries always carry a reason from the policy response,
// so the dash is only ever seen for allowed/no-violation entries.
func reasonOrDash(reason string) string {
	if cleaned := sanitizeCell(reason); cleaned != "" {
		return cleaned
	}
	return emptyReason
}
