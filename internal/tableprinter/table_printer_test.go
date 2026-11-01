//go:build !integration

package tableprinter

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/text"
)

func Test_ttyTablePrinter_truncate(t *testing.T) {
	buf := bytes.Buffer{}
	tp := NewTablePrinter()
	tp.SetTTYSeparator(" ")
	tp.SetTerminalWidth(5)
	tp.SetIsTTY(true)

	tp.AddCell("1")
	tp.AddCell("hello")
	tp.EndRow()
	tp.AddCell("2")
	tp.AddCell("world")
	tp.EndRow()

	buf.Write(tp.Bytes())

	expected := "1 h...\n2 w...\n"
	if buf.String() != expected {
		t.Errorf("expected: %q, got: %q", expected, buf.Bytes())
	}
}

func Test_nonTTYTablePrinter_truncate(t *testing.T) {
	buf := bytes.Buffer{}
	tp := NewTablePrinter()
	tp.SetTerminalWidth(5)
	tp.SetIsTTY(false)

	tp.AddCell("1")
	tp.AddCell("hello")
	tp.EndRow()
	tp.AddCell("2")
	tp.AddCell("world")
	tp.EndRow()

	buf.Write(tp.Bytes())

	expected := "1\thello\n2\tworld\n"
	if buf.String() != expected {
		t.Errorf("expected: %q, got: %q", expected, buf.Bytes())
	}
}

func TestTablePrinter(t *testing.T) {
	t.Run("nil pointer cell value", func(t *testing.T) {
		var createdAt *time.Time
		var status *string
		updatedAt := time.Now().UTC()

		tp := NewTablePrinter()
		tp.AddRow("id:", 1)
		tp.AddRow("Status:", status)
		tp.AddRow("Created:", createdAt)
		tp.AddRow("Updated:", updatedAt)

		expected := fmt.Sprintf("id:\t1\nStatus:\t<nil>\nCreated:\t<nil>\nUpdated:\t%v\n", updatedAt)
		require.Equal(t, expected, tp.String())
	})
}

func Test_nonTTYTablePrinter_pad(t *testing.T) {
	tp := NewTablePrinter()
	tp.SetIsTTY(false)
	tp.SetPad(true)
	tp.SetTerminalWidth(200)

	tp.AddRow("PACKAGE", "VERSION", "STATUS", "REASON")
	tp.AddRow("@typescript-eslint/eslint-plugin", "8.18.0", "Blocked", "policy violation")
	tp.AddRow("lodash", "4.17.21", "Warning", "deprecated")

	expected := "" +
		"PACKAGE                          VERSION STATUS  REASON          \n" +
		"@typescript-eslint/eslint-plugin 8.18.0  Blocked policy violation\n" +
		"lodash                           4.17.21 Warning deprecated      \n"
	require.Equal(t, expected, tp.String())
}

func Test_nonTTYTablePrinter_pad_notTruncatedWhenNarrow(t *testing.T) {
	// a narrow terminal would cap columns on a TTY; on a non-TTY, padding must still
	// align without truncating, including columns that follow the over-wide one
	tp := NewTablePrinter()
	tp.SetIsTTY(false)
	tp.SetPad(true)
	tp.SetTerminalWidth(20)

	tp.AddRow("PACKAGE", "STATUS", "REASON")
	tp.AddRow("@typescript-eslint/eslint-plugin", "Blocked", "policy violation")

	expected := "" +
		"PACKAGE                          STATUS  REASON          \n" +
		"@typescript-eslint/eslint-plugin Blocked policy violation\n"
	require.Equal(t, expected, tp.String())
}

func Test_nonTTYTablePrinter_padDisabled_keepsTSV(t *testing.T) {
	tp := NewTablePrinter()
	tp.SetIsTTY(false)
	tp.SetTerminalWidth(5)

	tp.AddRow("1", "hello")
	tp.AddRow("2", "world")

	expected := "1\thello\n2\tworld\n"
	require.Equal(t, expected, tp.String())
}

func Test_nonTTYTablePrinter_pad_maxColWidth(t *testing.T) {
	// an explicit MaxColWidth bounds padded output too, so a cell wider than the cap
	// is truncated and the columns after it stay aligned
	tp := NewTablePrinter()
	tp.SetIsTTY(false)
	tp.SetPad(true)
	tp.MaxColWidth = 6

	tp.AddRow("PACKAGE", "VERSION", "STATUS")
	tp.AddRow("@typescript-eslint/eslint-plugin", "8.18.0", "Blocked")
	tp.AddRow("lodash", "4.17.21", "Warning")

	expected := "" +
		"PAC... VER... STATUS\n" +
		"@ty... 8.18.0 Blo...\n" +
		"lodash 4.1... War...\n"
	require.Equal(t, expected, tp.String())
}

func Test_ttyTablePrinter_pad_narrowerThanFirstColumn(t *testing.T) {
	// the terminal is narrower than the first column plus separators, so every
	// capped column bottoms out at zero width; padded TTY output must still be
	// aligned and fit the terminal instead of printing those cells unformatted
	tp := NewTablePrinter()
	tp.SetIsTTY(true)
	tp.SetPad(true)
	tp.SetTerminalWidth(34)

	tp.AddRow("PACKAGE", "VERSION", "STATUS", "REASON")
	tp.AddRow("@typescript-eslint/eslint-plugin", "8.18.0", "Blocked", "policy violation")
	tp.AddRow("lodash", "4.17.21", "Warning", "deprecated")

	var width int
	for line := range strings.SplitSeq(strings.TrimRight(tp.String(), "\n"), "\n") {
		require.LessOrEqualf(t, text.StringWidth(line), 34, "line %q exceeds terminal width", line)
		if width == 0 {
			width = text.StringWidth(line)
		}
		require.Equalf(t, width, text.StringWidth(line), "line %q not aligned to %d columns", line, width)
	}
}

func Test_ttyTablePrinter_pad_widthNotExceeded(t *testing.T) {
	tp := NewTablePrinter()
	tp.SetIsTTY(true)
	tp.SetPad(true)
	tp.SetTerminalWidth(40)

	tp.AddRow("PACKAGE", "VERSION", "STATUS", "REASON")
	tp.AddRow("@typescript-eslint/eslint-plugin", "8.18.0", "Blocked", "policy violation")
	tp.AddRow("lodash", "4.17.21", "Warning", "deprecated")

	var width int
	for line := range strings.SplitSeq(strings.TrimRight(tp.String(), "\n"), "\n") {
		require.LessOrEqualf(t, text.StringWidth(line), 40, "line %q exceeds terminal width", line)
		if width == 0 {
			width = text.StringWidth(line)
		}
		require.Equalf(t, width, text.StringWidth(line), "line %q not aligned to %d columns", line, width)
	}
}
