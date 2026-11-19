package cmdtest

import "regexp"

var relativeYears = regexp.MustCompile(`\d+ years`)

// NormalizeRelativeYears replaces numeric year counts in command output with X years.
func NormalizeRelativeYears(output string) string {
	return relativeYears.ReplaceAllString(output, "X years")
}
