package utils

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"github.com/google/shlex"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/browser"
	"gitlab.com/gitlab-org/cli/internal/run"
)

type MarkdownRenderOpts []glamour.TermRendererOption

// OpenInBrowser opens the url in a web browser based on OS and $BROWSER environment variable
func OpenInBrowser(url, browserType string) error {
	browseCmd, err := browser.Command(url, browserType)
	if err != nil {
		return err
	}
	return run.PrepareCmd(browseCmd).Run()
}

func SanitizePathName(path string) string {
	if !strings.HasPrefix(path, "/") {
		// Prefix the path with "/" ensures that filepath.Clean removes all `/..`
		// See rule 4 of filepath.Clean for more information: https://pkg.go.dev/path/filepath#Clean
		path = "/" + path
	}
	return filepath.Clean(path)
}

func RenderMarkdown(text, glamourStyle string) (string, error) {
	opts := MarkdownRenderOpts{
		glamour.WithStylePath(getStyle(glamourStyle)),
	}

	return renderMarkdown(text, opts)
}

func RenderMarkdownWithoutIndentations(text, glamourStyle string) (string, error) {
	opts := MarkdownRenderOpts{
		glamour.WithStylePath(getStyle(glamourStyle)),
		markdownWithoutIndentation(),
	}

	return renderMarkdown(text, opts)
}

// markdownParser must use the same extensions as glamour, so that a block is
// a paragraph here exactly when glamour treats it as one.
var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList)).Parser()

// collapseSoftLineBreaks replaces soft line breaks in paragraphs with spaces so
// that glamour can preserve intentional hard line breaks without also
// preserving the source document's line wrapping.
//
// Workaround for https://github.com/charmbracelet/glamour/issues/84: glamour
// either joins every line in a paragraph or preserves every newline.
//
// Every edit is made between two of a paragraph's Lines() segments, which give
// the exact byte range of each line with container markers and indentation
// already removed. Hard line breaks are left untouched: the line that follows
// one must keep its own indentation and container markers, otherwise removing
// them can turn a paragraph continuation into a new block (a heading, a
// blockquote, or a list). Inline content, including code spans, is never
// copied, and no character in the source is inspected to guess where a line or
// a block ends.
func collapseSoftLineBreaks(src []byte) []byte {
	doc := markdownParser.Parse(text.NewReader(src))

	type edit struct {
		start, stop int
		text        string
	}
	var edits []edit

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		// A paragraph in a tight list item or a tight definition description
		// is parsed as a TextBlock rather than a Paragraph, but glamour reflows
		// both the same way, so both have their soft breaks collapsed here.
		var lines *text.Segments
		switch block := n.(type) {
		case *ast.Paragraph:
			lines = block.Lines()
		case *ast.TextBlock:
			lines = block.Lines()
		default:
			return ast.WalkContinue, nil
		}
		if lines == nil || lines.Len() < 2 {
			return ast.WalkContinue, nil
		}

		breaks := paragraphBreaks(n, lines)

		for i := 0; i < lines.Len()-1; i++ {
			kind := breaks[i]
			// A hard break needs no rewriting. Its marker is valid Markdown
			// and the following line stays exactly as the author wrote it.
			if kind == breakHard {
				continue
			}

			// The edited range runs from the end of this line's text to the
			// start of the next line's text, so the line ending and the next
			// line's container markers and indentation are replaced by one
			// space. A break inside a code span is not a line break: its line
			// ending becomes a space, but its trailing whitespace is content
			// and must be kept.
			start := lineContentEnd(src, lines.At(i))
			if kind == breakSoft {
				for start > lines.At(i).Start && (src[start-1] == ' ' || src[start-1] == '	') {
					start--
				}
			}
			edits = append(edits, edit{
				start: start,
				stop:  lines.At(i + 1).Start,
				text:  " ",
			})
		}
		return ast.WalkContinue, nil
	})

	if len(edits) == 0 {
		return src
	}

	// Apply from the end so earlier byte offsets stay valid.
	out := src
	for _, e := range slices.Backward(edits) {
		replaced := make([]byte, 0, len(out)-(e.stop-e.start)+len(e.text))
		replaced = append(replaced, out[:e.start]...)
		replaced = append(replaced, e.text...)
		replaced = append(replaced, out[e.stop:]...)
		out = replaced
	}
	return out
}

// breakKind classifies the line ending that the parser recorded at the end of
// a paragraph line, if any.
type breakKind int

const (
	breakNone breakKind = iota
	breakSoft
	breakHard
)

// paragraphBreaks reports, for each of the paragraph's lines, what the parser
// recorded at its end. A break is attributed to the line whose segment contains
// the end of the text node that carries it. A line ending inside a code span
// has no text node of its own, so it stays breakNone: CommonMark normalizes a
// line ending in a code span to a space, and that is the only rewrite it gets.
func paragraphBreaks(paragraph ast.Node, lines *text.Segments) []breakKind {
	breaks := make([]breakKind, lines.Len())
	_ = ast.Walk(paragraph, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		textNode, ok := n.(*ast.Text)
		if !ok || (!textNode.HardLineBreak() && !textNode.SoftLineBreak()) {
			return ast.WalkContinue, nil
		}
		kind := breakSoft
		if textNode.HardLineBreak() {
			kind = breakHard
		}
		for i := 0; i < lines.Len(); i++ {
			if textNode.Segment.Stop <= lines.At(i).Stop {
				breaks[i] = kind
				break
			}
		}
		return ast.WalkContinue, nil
	})
	return breaks
}

// lineContentEnd returns the offset just past a paragraph line's content,
// excluding its line ending. goldmark includes the line ending in every line
// segment except the block's last one.
func lineContentEnd(src []byte, segment text.Segment) int {
	stop := segment.Stop
	for stop > segment.Start && (src[stop-1] == '\n' || src[stop-1] == '\r') {
		stop--
	}
	return stop
}

func renderMarkdown(text string, opts MarkdownRenderOpts) (string, error) {
	// Glamour rendering preserves carriage return characters in code blocks, but
	// we need to ensure that no such characters are present in the output.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = string(collapseSoftLineBreaks([]byte(text)))

	// Glamour joins every line in a paragraph unless newlines are preserved.
	// Collapsing soft breaks above leaves only intentional hard breaks, which
	// must survive the paragraph reflow.
	opts = append(opts, glamour.WithPreservedNewLines())

	tr, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return "", err
	}

	return tr.Render(text)
}

func markdownWithoutIndentation() glamour.TermRendererOption {
	overrides := []byte(`
	  {
			"document": {
				"margin": 0
			},
			"code_block": {
				"margin": 0
			}
	  }`)

	return glamour.WithStylesFromJSONBytes(overrides)
}

func getStyle(glamourStyle string) string {
	if glamourStyle == "" || glamourStyle == "none" {
		return "notty"
	}
	return glamourStyle
}

func Pluralize(num int, thing string) string {
	if num == 1 {
		return fmt.Sprintf("%d %s", num, thing)
	}
	return fmt.Sprintf("%d %ss", num, thing)
}

func fmtDuration(amount int, unit string) string {
	return fmt.Sprintf("about %s ago", Pluralize(amount, unit))
}

func PrettyTimeAgo(ago time.Duration) string {
	if ago < time.Minute {
		return "less than a minute ago"
	}
	if ago < time.Hour {
		return fmtDuration(int(ago.Minutes()), "minute")
	}
	if ago < 24*time.Hour {
		return fmtDuration(int(ago.Hours()), "hour")
	}
	if ago < 30*24*time.Hour {
		return fmtDuration(int(ago.Hours())/24, "day")
	}
	if ago < 365*24*time.Hour {
		return fmtDuration(int(ago.Hours())/24/30, "month")
	}

	return fmtDuration(int(ago.Hours()/24/365), "year")
}

func TimeToPrettyTimeAgo(d time.Time) string {
	now := time.Now()
	ago := now.Sub(d)
	return PrettyTimeAgo(ago)
}

func FmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	return fmt.Sprintf("%02dm %02ds", m, s)
}

func Humanize(s string) string {
	// Replaces - and _ with spaces.
	replace := "_-"
	h := func(r rune) rune {
		if strings.ContainsRune(replace, r) {
			return ' '
		}
		return r
	}

	return strings.Map(h, s)
}

func DisplayURL(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}
	return u.Hostname() + u.Path
}

// PresentInStringSlice take a Hay (Slice of Strings) and a Needle (string)
// and returns true based on whether or not the Needle is present in the hay.
func PresentInStringSlice(hay []string, needle string) bool {
	return slices.Contains(hay, needle)
}

// CommonElementsInStringSlice takes 2 Slices of Strings and returns a Third Slice
// that is the common elements between the first 2 Slices.
func CommonElementsInStringSlice(s1 []string, s2 []string) []string {
	hash := make(map[string]bool)
	for x := range s1 {
		hash[s1[x]] = true
	}
	var arr []string
	for i := range s2 {
		if hash[s2[i]] {
			arr = append(arr, s2[i])
		}
	}
	return arr
}

// IsValidUrl tests a string to determine if it is a well-structured url or not.
func IsValidURL(toTest string) bool {
	_, err := url.ParseRequestURI(toTest)
	if err != nil {
		return false
	}

	u, err := url.Parse(toTest)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	return true
}

// Map transfers the elements of its first argument using the result of the second fn(e)
func Map[T1, T2 any](elems []T1, fn func(T1) T2) []T2 {
	r := make([]T2, len(elems))

	for i, v := range elems {
		r[i] = fn(v)
	}

	return r
}

// IsEnvVarEnabled checks if an environment variable is set
// and logs a warning to stderr if a boolean value cannot be parsed.
func IsEnvVarEnabled(key string) (bool, bool) {
	str, found := os.LookupEnv(key)
	if found {
		val, err := strconv.ParseBool(str)
		// if the environment variable has invalid value we print a warning
		// otherwise we return the parsed value
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Could not parse %s environment variable value: %s\n", key, err.Error())
		}

		if !strings.HasPrefix(key, "GLAB_") {
			PrintDeprecationWarning(key)
		}
		return val, found
	}

	return false, false
}

// PrintDeprecationWarning prints a deprecation warning to stderr to use the
// `GLAB_` prefix with environment variables. Diagnostics go to stderr so they
// do not pollute stdout pipelines — see gitlab-org/cli#8371.
func PrintDeprecationWarning(key string) {
	fmt.Fprintf(os.Stderr, "DEPRECATION WARNING: The environment variable %s has been deprecated and will be removed in future releases. Use GLAB_%s instead.\n", key, key)
}

// FormatDueDate returns an empty string if date is nil
func FormatDueDate(date *gitlab.ISOTime) string {
	if date == nil {
		return ""
	}
	return date.String()
}

// FormatTimeStats summarizes time tracking, for example "1h 30m spent of 2d
// estimated". It returns an empty string when nothing is tracked.
func FormatTimeStats(ts *gitlab.TimeStats) string {
	if ts == nil {
		return ""
	}
	var parts []string
	if ts.HumanTotalTimeSpent != "" {
		parts = append(parts, ts.HumanTotalTimeSpent+" spent")
	}
	if ts.HumanTimeEstimate != "" {
		parts = append(parts, ts.HumanTimeEstimate+" estimated")
	}
	return strings.Join(parts, " of ")
}

// timeTrackingUnits uses the GitLab time tracking conversion rates, which
// count a working day of 8 hours rather than a calendar day.
var timeTrackingUnits = []struct {
	suffix  string
	seconds int64
}{
	{"mo", 4 * 5 * 8 * 3600},
	{"w", 5 * 8 * 3600},
	{"d", 8 * 3600},
	{"h", 3600},
	{"m", 60},
	{"s", 1},
}

// FormatTimeSpent formats a number of seconds as GitLab displays tracked
// time, for example "1w 2d 3h 30m". Negative values keep a leading "-".
func FormatTimeSpent(seconds int64) string {
	if seconds == 0 {
		return "0h"
	}
	sign := ""
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	var parts []string
	for _, unit := range timeTrackingUnits {
		if n := seconds / unit.seconds; n > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", n, unit.suffix))
			seconds %= unit.seconds
		}
	}
	return sign + strings.Join(parts, " ")
}

// ParseEditorCommand parses an editor command string into separate command and arguments.
// This is useful when passing editor commands to libraries like huh that expect separate
// command and argument parameters.
//
// For example:
//   - Input: "emacsclient -t -a \"emacs -nw\""
//   - Output: []string{"emacsclient", "-t", "-a", "emacs -nw"}
//
// If parsing fails, it returns a slice containing just the original string.
func ParseEditorCommand(editorCmd string) []string {
	if editorCmd == "" {
		return nil
	}

	parts, err := shlex.Split(editorCmd)
	if err != nil || len(parts) == 0 {
		// Fall back to returning the full string if parsing fails
		return []string{editorCmd}
	}

	return parts
}

// ReplaceNonAlphaNumericChars replaces non alpha-numeric values with provided char/string
func ReplaceNonAlphaNumericChars(words, replaceWith string) string {
	reg := regexp.MustCompile("[^A-Za-z0-9]+")
	newStr := reg.ReplaceAllString(strings.Trim(words, " "), replaceWith)
	return newStr
}

// StringToInt converts a string to an integer, returning 0 if conversion fails
func StringToInt(str string) int {
	strInt, err := strconv.Atoi(str)
	if err != nil {
		return 0
	}
	return strInt
}
