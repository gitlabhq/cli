//go:build !integration

package utils

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func Test_RenderMarkdownLineBreaks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "hard break with two spaces",
			input: "first  \nsecond",
			want:  []string{"first", "second"},
		},
		{
			name:  "hard break with backslash",
			input: "first\\\nsecond",
			want:  []string{"first", "second"},
		},
		{
			name:  "soft break collapses",
			input: "first\nsecond",
			want:  []string{"first second"},
		},
		{
			name:  "soft break with one trailing space collapses",
			input: "first \nsecond",
			want:  []string{"first second"},
		},
		{
			name:  "mixed hard and soft breaks",
			input: "first  \nsecond\nthird",
			want:  []string{"first", "second third"},
		},
		{
			name:  "soft break in blockquote collapses",
			input: "> first\n> second",
			want:  []string{"first second"},
		},
		{
			name:  "hard break in blockquote",
			input: "> first  \n> second",
			want:  []string{"first", "second"},
		},
		{
			name:  "soft break in tight list item collapses",
			input: "- first\n  second",
			want:  []string{"first second"},
		},
		{
			name:  "hard break in list item",
			input: "- first  \n  second",
			want:  []string{"first", "second"},
		},
		{
			name:  "fenced code keeps hard breaks",
			input: "```\nfirst  \nsecond\n```",
			want:  []string{"first", "second"},
		},
		{
			name:  "inline code is not a hard break",
			input: "`first\nsecond`",
			want:  []string{"first second"},
		},
		{
			name:  "crlf hard break",
			input: "first  \r\nsecond",
			want:  []string{"first", "second"},
		},
		{
			// A newline inside a code span is not a line break in CommonMark.
			name:  "inline code spanning a newline stays on one line",
			input: "a `b\nc` d",
			want:  []string{"a b c d"},
		},
		{
			name:  "term and definition form a definition list",
			input: "Term\n: Definition",
			want:  []string{"Term", "Definition"},
		},
		{
			name:  "indented block keeps its angle bracket",
			input: "a\n    > not quote",
			want:  []string{"a > not quote"},
		},
		{
			name:  "list item indented block keeps its angle bracket",
			input: "- a\n      > b",
			want:  []string{"a > b"},
		},
		{
			name:  "soft break in definition description collapses",
			input: "Term\n: first\n  second",
			want:  []string{"Term", "first second"},
		},
		{
			// A hard break keeps the next line in the paragraph, so the
			// indentation that makes ">" literal must survive the rewrite.
			name:  "hard break before indented blockquote stays literal",
			input: "a  \n    > not quote",
			want:  []string{"a", "> not quote"},
		},
		{
			name:  "hard break before indented heading stays literal",
			input: "a  \n    # not heading",
			want:  []string{"a", "# not heading"},
		},
		{
			// Removing the indentation would turn this continuation into a
			// setext heading underline for the preceding line.
			name:  "hard break before indented dashes is not a heading",
			input: "a  \n    ---",
			want:  []string{"a", "---"},
		},
		{
			name:  "hard break before indented list marker stays literal",
			input: "a  \n    - not a list",
			want:  []string{"a", "- not a list"},
		},
		{
			name:  "hard break before indented continuation in list item",
			input: "- a  \n      > b",
			want:  []string{"a", "> b"},
		},
		{
			name:  "heading and following text are separate lines",
			input: "# head  \ntext",
			want:  []string{"# head", "text"},
		},
		{
			name:  "blockquote heading is not lazily continued by unmarked text",
			input: "> # head  \ntext",
			want:  []string{"| # head", "text"},
		},
		{
			name:  "blockquote in list item keeps its hard break",
			input: "- > quote  \n  > cont",
			want:  []string{"•", "| quote", "| cont"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered, err := RenderMarkdown(tt.input, "notty")
			require.NoError(t, err)

			lines := nonEmptyTrimmedLines(rendered)
			require.Len(t, lines, len(tt.want))
			for i, want := range tt.want {
				require.Contains(t, lines[i], want)
			}
		})
	}
}

// Test_CollapseSoftLineBreaks checks the rewritten Markdown itself. A hard line
// break must leave the source untouched: the line following it keeps the
// indentation and container markers that keep it a paragraph continuation.
func Test_CollapseSoftLineBreaks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "soft break becomes a space",
			input: "first\nsecond",
			want:  "first second",
		},
		{
			name:  "soft break drops trailing space and indentation",
			input: "first \n    second",
			want:  "first second",
		},
		{
			name:  "hard break with two spaces is untouched",
			input: "first  \nsecond",
			want:  "first  \nsecond",
		},
		{
			name:  "hard break with backslash is untouched",
			input: "first\\\nsecond",
			want:  "first\\\nsecond",
		},
		{
			name:  "indented continuation after hard break is untouched",
			input: "a  \n    > not quote",
			want:  "a  \n    > not quote",
		},
		{
			name:  "indented heading after hard break is untouched",
			input: "a  \n    # not heading",
			want:  "a  \n    # not heading",
		},
		{
			name:  "indented setext underline after hard break is untouched",
			input: "a  \n    ---",
			want:  "a  \n    ---",
		},
		{
			name:  "indented list marker after hard break is untouched",
			input: "a  \n    - not a list",
			want:  "a  \n    - not a list",
		},
		{
			name:  "hard break inside blockquote is untouched",
			input: "> a  \n> b",
			want:  "> a  \n> b",
		},
		{
			name:  "soft break inside blockquote collapses",
			input: "> a\n> b",
			want:  "> a b",
		},
		{
			name:  "lazy blockquote continuation collapses",
			input: "> a\nb",
			want:  "> a b",
		},
		{
			name:  "hard break inside list item is untouched",
			input: "- a  \n  b",
			want:  "- a  \n  b",
		},
		{
			name:  "soft break inside list item collapses",
			input: "- a\n  b",
			want:  "- a b",
		},
		{
			name:  "indented code after heading in blockquote is untouched",
			input: "> # a  \n>     b",
			want:  "> # a  \n>     b",
		},
		{
			name:  "hard break inside blockquote in list is untouched",
			input: "- > a  \n  > b",
			want:  "- > a  \n  > b",
		},
		{
			name:  "only the soft break of a mixed paragraph changes",
			input: "one\ntwo  \nthree",
			want:  "one two  \nthree",
		},
		{
			name:  "hard break followed by a soft break",
			input: "one  \ntwo\nthree",
			want:  "one  \ntwo three",
		},
		{
			name:  "newline inside code span becomes a space",
			input: "a `b\nc` d",
			want:  "a `b c` d",
		},
		{
			name:  "code span keeps its trailing spaces",
			input: "a `b  \nc` d",
			want:  "a `b   c` d",
		},
		{
			name:  "fenced code is untouched",
			input: "```\na  \nb\n```",
			want:  "```\na  \nb\n```",
		},
		{
			name:  "heading and following text are left as written",
			input: "# a  \ntext",
			want:  "# a  \ntext",
		},
		{
			name:  "soft break inside tight list item among siblings collapses",
			input: "- a\n  b\n- c",
			want:  "- a b\n- c",
		},
		{
			name:  "soft break in definition description collapses",
			input: "Term\n: def\n  cont",
			want:  "Term\n: def cont",
		},
		{
			name:  "hard break in definition description is untouched",
			input: "Term\n: def  \n  cont",
			want:  "Term\n: def  \n  cont",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, string(collapseSoftLineBreaks([]byte(tt.input))))
		})
	}
}

func nonEmptyTrimmedLines(s string) []string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func Test_PrettyTimeAgo(t *testing.T) {
	cases := map[string]string{
		"1s":         "less than a minute ago",
		"30s":        "less than a minute ago",
		"1m08s":      "about 1 minute ago",
		"15m0s":      "about 15 minutes ago",
		"59m10s":     "about 59 minutes ago",
		"1h10m02s":   "about 1 hour ago",
		"15h0m01s":   "about 15 hours ago",
		"30h10m":     "about 1 day ago",
		"50h":        "about 2 days ago",
		"720h05m":    "about 1 month ago",
		"3000h10m":   "about 4 months ago",
		"8760h59m":   "about 1 year ago",
		"17601h59m":  "about 2 years ago",
		"262800h19m": "about 30 years ago",
	}

	for duration, expected := range cases {
		d, e := time.ParseDuration(duration)
		if e != nil {
			t.Errorf("failed to create a duration: %s", e)
		}

		fuzzy := PrettyTimeAgo(d)
		require.Equal(t, expected, fuzzy, "unexpected fuzzy duration value: %s for %s")
	}
}

func Test_Pluralize(t *testing.T) {
	testCases := []struct {
		name   string
		word   string
		amount int
		want   string
	}{
		{
			name:   "singular",
			word:   "label",
			amount: 1,
			want:   "1 label",
		},
		{
			name:   "plural",
			word:   "label",
			amount: 3,
			want:   "3 labels",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := Pluralize(tC.amount, tC.word)
			require.Equal(t, tC.want, got, "Pluralize() got = %s, want = %s")
		})
	}
}

func Test_PresentInStringSlice(t *testing.T) {
	testCases := []struct {
		name   string
		hay    []string
		needle string
		want   bool
	}{
		{"simple true", []string{"foo", "bar", "baz"}, "bar", true},
		{"simple false", []string{"foo", "bar", "baz"}, "qux", false},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := PresentInStringSlice(tC.hay, tC.needle)
			require.Equal(t, tC.want, got, "PresentInStringSlice() got = %t, want = %t")
		})
	}
}

func Test_SanitizePathName(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "A regular filename",
			filename: "cli-v1.22.0.json",
			want:     "/cli-v1.22.0.json",
		},
		{
			name:     "A regular filename in a directory",
			filename: "cli/cli-v1.22.0.json",
			want:     "/cli/cli-v1.22.0.json",
		},
		{
			name:     "A filename with directory traversal",
			filename: "cli-v1.../../22.0.zip",
			want:     "/22.0.zip",
		},
		{
			name:     "A particularly nasty filename",
			filename: "..././..././..././etc/password_file",
			want:     "/.../.../.../etc/password_file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePathWanted := SanitizePathName(tt.filename)
			require.Equal(t, filePathWanted, tt.want, "SanitizePathName() got = %s, want = %s")
		})
	}
}

func Test_CommonElementsInStringSlice(t *testing.T) {
	testCases := []struct {
		name   string
		array1 []string
		array2 []string
		want   []string
	}{
		{
			name:   "simple no matching elements",
			array1: []string{"foo", "bar", "baz"},
			array2: []string{"qux", "quux", "quz"},
			want:   []string{},
		},
		{
			name:   "simple matching elements",
			array1: []string{"foo", "quux", "baz"},
			array2: []string{"qux", "quux", "baz"},
			want:   []string{"quux", "baz"},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := CommonElementsInStringSlice(tC.array1, tC.array2)
			require.Len(t, tC.want, len(got), "CommonElementsInStringSlice() size of got (%d) and wanted (%d) arrays differ")
			for i := range got {
				require.Equal(t, got[i], tC.want[i], "CommonElementsInStringSlice() got = %s, want = %s")
			}
		})
	}
}

func Test_Map(t *testing.T) {
	type SomeType struct {
		name string
	}

	type Tests[T1, T2 any] struct {
		name  string
		slice []T1
		fn    func(T1) T2
		want  []T2
	}

	tests := []Tests[any, any]{
		{
			"list of strings",
			[]any{"foo", "bar", "baz"},
			func(e any) any { return e },
			[]any{"foo", "bar", "baz"},
		},
		{
			"list of structs",
			[]any{SomeType{"foo"}, SomeType{"bar"}, SomeType{"baz"}},
			func(e any) any { s, _ := e.(SomeType); return s.name },
			[]any{"foo", "bar", "baz"},
		},
		{
			"no elements",
			[]any{},
			func(e any) any { return e },
			[]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Map(tt.slice, tt.fn)
			require.Equal(t, tt.want, got, "Test_Map() want %v; but got %v")
		})
	}
}

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "invalid empty url",
			input:    "",
			expected: false,
		},
		{
			name:     "invalid url without correct scheme",
			input:    "https:/gitlab.com/group",
			expected: false,
		},
		{
			name:     "ok with correct url",
			input:    "https://gitlab.com/group",
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualIsValid := IsValidURL(test.input)
			require.Equal(t, test.expected, actualIsValid, "TestIsValidURL() got = %s, want = %s")
		})
	}
}

func TestParseEditorCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		editor   string
		expected []string
	}{
		{
			name:     "empty string",
			editor:   "",
			expected: nil,
		},
		{
			name:     "simple editor",
			editor:   "vim",
			expected: []string{"vim"},
		},
		{
			name:     "editor with flags",
			editor:   "nano -w",
			expected: []string{"nano", "-w"},
		},
		{
			name:     "emacsclient with flags",
			editor:   "emacsclient -t -a emacs",
			expected: []string{"emacsclient", "-t", "-a", "emacs"},
		},
		{
			name:     "emacsclient with quoted fallback",
			editor:   `emacsclient -t -a "emacs -nw"`,
			expected: []string{"emacsclient", "-t", "-a", "emacs -nw"},
		},
		{
			name:     "vscode with flags",
			editor:   `code --wait --new-window`,
			expected: []string{"code", "--wait", "--new-window"},
		},
		{
			name:     "vim with complex flags",
			editor:   "vim -c 'set spell'",
			expected: []string{"vim", "-c", "set spell"},
		},
		{
			name:     "full path editor",
			editor:   "/usr/local/bin/nvim -u NONE",
			expected: []string{"/usr/local/bin/nvim", "-u", "NONE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseEditorCommand(tt.editor)
			require.Equal(t, tt.expected, got)
		})
	}
}

func Test_HelperFuncs(t *testing.T) {
	t.Run("StringToInt()", func(t *testing.T) {
		t.Run("success", func(t *testing.T) {
			got := StringToInt("200")
			require.Equal(t, 200, got)
		})
		t.Run("failed-return-0", func(t *testing.T) {
			got := StringToInt("NotAnInt")
			require.Equal(t, 0, got)
		})
	})
	t.Run("ReplaceNonAlphaNumericChars()", func(t *testing.T) {
		got := ReplaceNonAlphaNumericChars("profclems-glab", "/")
		require.Equal(t, "profclems/glab", got)
	})
}

func TestFormatTimeStats(t *testing.T) {
	tests := []struct {
		name string
		ts   *gitlab.TimeStats
		want string
	}{
		{name: "nil", ts: nil, want: ""},
		{name: "nothing tracked", ts: &gitlab.TimeStats{}, want: ""},
		{
			name: "time spent only",
			ts:   &gitlab.TimeStats{HumanTotalTimeSpent: "1h 30m", TotalTimeSpent: 5400},
			want: "1h 30m spent",
		},
		{
			name: "estimate only",
			ts:   &gitlab.TimeStats{HumanTimeEstimate: "2d", TimeEstimate: 57600},
			want: "2d estimated",
		},
		{
			name: "time spent and estimate",
			ts: &gitlab.TimeStats{
				HumanTotalTimeSpent: "1h 30m", TotalTimeSpent: 5400,
				HumanTimeEstimate: "2d", TimeEstimate: 57600,
			},
			want: "1h 30m spent of 2d estimated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, FormatTimeStats(tt.ts))
		})
	}
}

func TestFormatTimeSpent(t *testing.T) {
	tests := []struct {
		name    string
		seconds int64
		want    string
	}{
		{name: "zero", seconds: 0, want: "0h"},
		{name: "minutes", seconds: 45 * 60, want: "45m"},
		{name: "hours and minutes", seconds: 5400, want: "1h 30m"},
		{name: "a working day is 8 hours", seconds: 8 * 3600, want: "1d"},
		{name: "a week is 5 working days", seconds: 5*8*3600 + 2*8*3600 + 3*3600 + 30*60, want: "1w 2d 3h 30m"},
		{name: "a month is 4 weeks", seconds: 4 * 5 * 8 * 3600, want: "1mo"},
		{name: "seconds", seconds: 90, want: "1m 30s"},
		{name: "negative", seconds: -5400, want: "-1h 30m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, FormatTimeSpent(tt.seconds))
		})
	}
}
