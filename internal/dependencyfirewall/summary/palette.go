package summary

import (
	"fmt"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// palette renders the summary's colors by delegating to an iostreams
// ColorPalette, so the ANSI escapes (truecolor, 256-color, NO_COLOR handling)
// stay owned by iostreams. The palette is sourced from ColorForCILogs so the
// box keeps its color in GitLab CI job logs, which are not a TTY.
type palette struct {
	c *iostreams.ColorPalette
}

func newPalette(io *iostreams.IOStreams) palette {
	return palette{c: io.ColorForCILogs()}
}

// accent returns the colorizer for the box border and title, keyed off the
// worst verdict in the run.
func (p palette) accent(v verdict.Verdict) func(string) string {
	switch v {
	case verdict.Blocked:
		return p.c.Red
	case verdict.Warning:
		return p.c.Yellow
	default:
		return p.c.Green
	}
}

// icon returns the colored status glyph for a verdict, reusing iconFor for the
// glyph so the mapping lives in one place.
func (p palette) icon(v verdict.Verdict) string {
	return p.accent(v)(iconFor(v))
}

// summary renders the count line with the counts colored by severity.
func (p palette) summary(total, blocked, warned int) string {
	switch {
	case blocked > 0 && warned > 0:
		return fmt.Sprintf("%s, %s",
			p.c.Red(fmt.Sprintf("%d blocked", blocked)),
			p.c.Yellow(fmt.Sprintf("%d warning", warned)))
	case blocked == 1 && warned == 0:
		return p.c.Red("1 package blocked")
	case blocked > 1 && warned == 0:
		return p.c.Red(fmt.Sprintf("%d packages blocked", blocked))
	case warned == 1 && blocked == 0:
		return p.c.Yellow("1 package warning")
	case warned > 1 && blocked == 0:
		return p.c.Yellow(fmt.Sprintf("%d packages warning", warned))
	default:
		return p.c.Green(fmt.Sprintf("%d package(s) recorded", total))
	}
}
