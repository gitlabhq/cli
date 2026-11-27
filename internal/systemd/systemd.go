// Package systemd renders values for systemd unit files.
package systemd

import "strings"

// quoter escapes "%" as well as the C escapes because systemd expands %
// specifiers even inside a quoted setting.
var (
	quoter   = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%")
	unquoter = strings.NewReplacer(`\\`, `\`, `\"`, `"`, "%%", "%")
)

// Quote escapes s for use between the double quotes of a unit setting such as
// Environment= or ExecStart=.
func Quote(s string) string {
	return quoter.Replace(s)
}

// Unquote reverses Quote.
func Unquote(s string) string {
	return unquoter.Replace(s)
}
