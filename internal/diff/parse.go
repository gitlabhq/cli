// Package diff parses unified diff text to build line mappings.
package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// LineType describes what happened to a line.
type LineType int

const (
	Unchanged LineType = iota
	Added
	Removed
)

// Line represents a single line in a diff hunk.
type Line struct {
	Type    LineType
	OldLine int // 0 if added
	NewLine int // 0 if removed
}

var hunkRe = regexp.MustCompile(`^@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@`)

// Parse parses a unified diff fragment (the "diff" field from GitLab API)
// and returns a list of Line entries.
func Parse(diffText string) []Line {
	var lines []Line
	var oldLine, newLine int
	inHunk := false

	for raw := range strings.SplitSeq(strings.TrimRight(diffText, "\n"), "\n") {
		if m := hunkRe.FindStringSubmatch(raw); m != nil {
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[2])
			inHunk = true
			continue
		}
		if strings.HasPrefix(raw, "\\ No newline") {
			continue
		}
		if raw == "" {
			if inHunk {
				// Empty line inside a hunk = blank context line
				// (trailing whitespace may have been stripped from " ")
				lines = append(lines, Line{Type: Unchanged, OldLine: oldLine, NewLine: newLine})
				oldLine++
				newLine++
			}
			continue
		}

		switch raw[0] {
		case '+':
			lines = append(lines, Line{Type: Added, NewLine: newLine})
			newLine++
		case '-':
			lines = append(lines, Line{Type: Removed, OldLine: oldLine})
			oldLine++
		default:
			// context line (starts with ' ' or no prefix)
			lines = append(lines, Line{Type: Unchanged, OldLine: oldLine, NewLine: newLine})
			oldLine++
			newLine++
		}
	}
	return lines
}

// FindNewLine looks up a new-side line number in the parsed lines and returns
// the corresponding old line number and the line type.
// Returns an error if the line is not found in the diff.
func FindNewLine(lines []Line, target int) (int, LineType, error) {
	for _, l := range lines {
		if l.NewLine == target && l.Type != Removed {
			return l.OldLine, l.Type, nil
		}
	}
	return 0, 0, fmt.Errorf("new line %d not found in diff", target)
}

// FindOldLine looks up an old-side line number in the parsed lines and returns
// the corresponding new line number and the line type.
func FindOldLine(lines []Line, target int) (int, LineType, error) {
	for _, l := range lines {
		if l.OldLine == target && l.Type != Added {
			return l.NewLine, l.Type, nil
		}
	}
	return 0, 0, fmt.Errorf("old line %d not found in diff", target)
}
