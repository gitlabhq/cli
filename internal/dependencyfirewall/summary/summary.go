package summary

import (
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/text"
)

const title = "GitLab Dependency Firewall"

// maxPackageWidth caps the PACKAGE column so a single long, scoped package
// name can't push the REASON column off-screen (or wrap in CI job logs).
// Names longer than this are truncated with an ellipsis; REASON is never
// truncated.
const maxPackageWidth = 20

// Render writes a summary of Dependency Firewall verdicts. Blocked and warned
// packages are listed in a padded columnar table (via text/tabwriter) so the
// output stays aligned in both interactive terminals and CI job logs, which
// don't render tab stops.
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

	body := renderBody(io, entries, blocked, warned)
	if blocked > 0 {
		io.LogError(body)
	} else {
		io.LogInfo(body)
	}
}

func renderBody(io *iostreams.IOStreams, entries []verdict.Entry, blocked, warned int) string {
	c := io.Color()
	bold := c.Bold

	// Build a plain-text table first (no ANSI in the aligned columns),
	// because text/tabwriter counts bytes and ANSI escapes would throw
	// alignment off. Compute widths ourselves so we can colour the
	// STATUS cell after padding.
	type row struct {
		v                        verdict.Verdict
		pkg, ver, status, reason string
	}
	rows := []row{{pkg: "PACKAGE", ver: "VERSION", status: "STATUS", reason: "REASON"}}
	for _, e := range entries {
		rows = append(rows, row{
			v:      e.Verdict,
			pkg:    e.Package,
			ver:    e.Version,
			status: statusLabel(e.Verdict),
			reason: reasonOrDefault(e.Reason),
		})
	}

	wPkg, wVer, wStatus := 0, 0, 0
	for _, r := range rows {
		if len(r.pkg) > wPkg {
			wPkg = len(r.pkg)
		}
		if wPkg > maxPackageWidth {
			wPkg = maxPackageWidth
		}
		if len(r.ver) > wVer {
			wVer = len(r.ver)
		}
		if len(r.status) > wStatus {
			wStatus = len(r.status)
		}
	}

	var tbl strings.Builder
	for i, r := range rows {
		var status string
		if i == 0 {
			status = padRight(r.status, wStatus)
		} else {
			// Colour the label, then pad with plain spaces so column width
			// (which is measured in visible characters) stays right.
			status = colourStatus(c, r.v, r.status) + strings.Repeat(" ", wStatus-len(r.status))
		}
		fmt.Fprintf(&tbl, "%s  %s  %s  %s\n",
			text.Truncate(r.pkg, wPkg),
			padRight(r.ver, wVer),
			status,
			r.reason,
		)
	}

	var b strings.Builder
	b.WriteByte('\n')
	b.WriteString(bold(title))
	b.WriteByte('\n')
	b.WriteString(summaryLine(len(entries), blocked, warned))
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(tbl.String())
	return b.String()
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func statusLabel(v verdict.Verdict) string {
	switch v {
	case verdict.Blocked:
		return "Blocked"
	case verdict.Warning:
		return "Warning"
	default:
		return string(v)
	}
}

// colourStatus keys off the typed verdict rather than the rendered label, so
// changing statusLabel's wording can't silently drop the colouring.
func colourStatus(c *iostreams.ColorPalette, v verdict.Verdict, label string) string {
	switch v {
	case verdict.Blocked:
		return c.Red(label)
	case verdict.Warning:
		return c.Yellow(label)
	default:
		return label
	}
}

func summaryLine(total, blocked, warned int) string {
	switch {
	case blocked > 0 && warned > 0:
		return fmt.Sprintf("%d issues found: %d blocked, %d warning", blocked+warned, blocked, warned)
	case blocked == 1 && warned == 0:
		return "1 package blocked"
	case blocked > 1 && warned == 0:
		return fmt.Sprintf("%d packages blocked", blocked)
	case warned == 1 && blocked == 0:
		return "1 package warning"
	case warned > 1 && blocked == 0:
		return fmt.Sprintf("%d packages warning", warned)
	default:
		return fmt.Sprintf("%d package(s) recorded", total)
	}
}

func reasonOrDefault(reason string) string {
	if reason == "" {
		return "(no reason provided)"
	}
	return reason
}
