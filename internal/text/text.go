package text

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

const ExperimentalString = `
This feature is an experiment and is not ready for production use.
It might be unstable or removed at any time.
For more information, see
https://docs.gitlab.com/policy/development_stages_support/.
`

const ExperimentalFlagString = `
The %s flag is an experiment. It might be
unstable or removed at any time, and is not ready for production use.
For more information, see
https://docs.gitlab.com/policy/development_stages_support/.
`

const BetaString = `
This feature is in beta and might not be ready for production use.
It might be unstable and breaking changes can occur outside of major releases.
For more information, see
https://docs.gitlab.com/policy/development_stages_support/.
`

const ansi = "[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))"

var re = regexp.MustCompile(ansi)

var hyperlinkOSCRegexp = regexp.MustCompile("\u001B\\]8;[^;]*;.*?\u001B\\\\")

// Join joins the list of the string with the delim provided.
// Returns an empty string for empty list
func Join(list []string, delim string) string {
	if len(list) == 0 {
		return ""
	}
	var buf bytes.Buffer
	for i := range len(list) - 1 {
		buf.WriteString(list[i] + delim)
	}
	buf.WriteString(list[len(list)-1])
	return buf.String()
}

// Strip strips the string of all colors
func Strip(s string) string {
	return re.ReplaceAllString(hyperlinkOSCRegexp.ReplaceAllString(s, ""), "")
}

// SanitizeInline makes an untrusted string safe to print on a single line of
// terminal or CI output. It removes ANSI/OSC escape sequences, drops the
// remaining C0/C1 control characters (bell, backspace, and the like that
// strings.Fields does not treat as whitespace), and collapses every run of
// whitespace to a single space. Use it on any value derived from network or
// user input before writing it to a log line, so a crafted value cannot inject
// newlines or terminal control sequences.
func SanitizeInline(s string) string {
	stripped := Strip(s)
	var b strings.Builder
	b.Grow(len(stripped))
	for _, r := range stripped {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// StringWidth returns the actual width of the string without colors
func StringWidth(s string) int {
	return runewidth.StringWidth(Strip(s))
}

// RuneWidth returns the actual width of the rune
func RuneWidth(s rune) int {
	return runewidth.RuneWidth(s)
}

func WrapString(text string, lineWidth int) string {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) == 0 {
		return text
	}
	wrapped := words[0]
	spaceLeft := lineWidth - StringWidth(wrapped)
	for _, word := range words[1:] {
		wordWidth := StringWidth(word)
		if wordWidth+1 > spaceLeft {
			wrapped += "\n" + word
			spaceLeft = lineWidth - wordWidth
		} else {
			wrapped += " " + word
			spaceLeft -= 1 + wordWidth
		}
	}
	return wrapped
}

// PadRight returns a new string of a specified length in which the end of the current string is padded with spaces or with a specified Unicode character.
func PadRight(str string, length int, pad byte) string {
	slen := StringWidth(str)
	if slen >= length {
		return str
	}
	buf := bytes.NewBufferString(str)
	for range length - slen {
		buf.WriteByte(pad)
	}
	return buf.String()
}

// PadLeft returns a new string of a specified length in which the beginning of the current string is padded with spaces or with a specified Unicode character.
func PadLeft(str string, length int, pad byte) string {
	slen := StringWidth(str)
	if slen >= length {
		return str
	}
	var buf bytes.Buffer
	for range length - slen {
		buf.WriteByte(pad)
	}
	buf.WriteString(str)
	return buf.String()
}
