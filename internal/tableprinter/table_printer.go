package tableprinter

import (
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/text"
)

var tp *TablePrinter

func init() {
	tp = &TablePrinter{
		TotalRows:       0,
		Wrap:            false,
		MaxColWidth:     0,
		TTYSeparator:    "\t",
		NonTTYSeparator: "\t",
		TerminalWidth:   80,
	}
}

// TablePrinter represents a decorator that renders the data formatted in a tabular form.
type TablePrinter struct {
	// Total number of records. Needed if AddRowFunc is used
	TotalRows int
	// Wrap when set to true wraps the contents of the columns when the length exceeds the MaxColWidth
	Wrap bool
	// MaxColWidth is the maximum allowed width for cells in the table
	MaxColWidth int
	// TTYSeparator is the separator for columns in the table on TTYs. Default is "\t"
	TTYSeparator string
	// NonTTYSeparator is the separator for columns in the table on non-TTYs. Default is "\t"
	NonTTYSeparator string
	// Rows is the collection of rows in the table
	Rows []*TableRow
	// TerminalWidth is the max width of the terminal
	TerminalWidth int
	// IsTTY indicates whether output is a TTY or non-TTY
	IsTTY bool
	// Pad, when true, pads cells to their column width independently of IsTTY, so
	// non-TTY output can be aligned without being truncated. It forces a
	// single-space column separator, overriding TTYSeparator/NonTTYSeparator.
	Pad bool
}

type TableCell struct {
	// Value in the cell
	Value any
	// Width is the width of the cell
	Width int
	// Wrap when true wraps the contents of the cell when the length exceeds the width
	Wrap bool

	isaTTY bool
	pad    bool
	// widthComputed records that Render assigned Width from colWidths.
	// It lets a padded cell distinguish a column capped to zero width
	// from a width that is simply not set yet: LineWidth measures cells
	// before widths exist and must see the raw value.
	widthComputed bool
}

type TableRow struct {
	Cells []*TableCell
	// Separator is the separator for columns in the table. Default is " "
	Separator string
}

func NewTablePrinter() *TablePrinter {
	t := &TablePrinter{
		TTYSeparator:    tp.TTYSeparator,
		NonTTYSeparator: tp.NonTTYSeparator,
		MaxColWidth:     tp.MaxColWidth,
		Wrap:            false,
		TerminalWidth:   tp.TerminalWidth,
		IsTTY:           tp.IsTTY,
		Pad:             tp.Pad,
	}

	return t
}

func (t *TablePrinter) Separator() string {
	// when padding, use a fixed-width space so a tab doesn't overshoot the computed widths
	if t.Pad {
		return " "
	}

	if t.IsTTY {
		return t.TTYSeparator
	}

	return t.NonTTYSeparator
}

// SetTerminalWidth sets the maximum width for the terminal
func SetTerminalWidth(width int) { tp.SetTerminalWidth(width) }

func (t *TablePrinter) SetTerminalWidth(width int) {
	t.TerminalWidth = width
}

// SetIsTTY sets the IsTTY variable which indicates whether terminal
// output is a TTY or nonTTY
func SetIsTTY(isTTY bool) { tp.SetIsTTY(isTTY) }

func (t *TablePrinter) SetIsTTY(isTTY bool) {
	t.IsTTY = isTTY
}

// SetPad sets the Pad variable which, when true, pads cells to their column
// width independently of IsTTY
func SetPad(pad bool) { tp.SetPad(pad) }

func (t *TablePrinter) SetPad(pad bool) {
	t.Pad = pad
}

// SetTTYSeparator sets the separator for the columns in the table for TTYs
func SetTTYSeparator(s string) { tp.SetTTYSeparator(s) }

func (t *TablePrinter) SetTTYSeparator(s string) {
	t.TTYSeparator = s
}

func (t *TablePrinter) makeRow() {
	if t.Rows == nil {
		t.Rows = make([]*TableRow, 1)
		t.Rows[0] = &TableRow{}
	}
}

func (t *TablePrinter) AddCell(s any) {
	t.makeRow()
	rowI := len(t.Rows) - 1
	row := t.Rows[rowI]

	cell := &TableCell{
		Value:  s,
		isaTTY: t.IsTTY,
		pad:    t.Pad,
	}

	row.Separator = t.Separator()

	row.Cells = append(row.Cells, cell)
}

// AddCellf formats according to a format specifier and adds cell to row
func (t *TablePrinter) AddCellf(s string, f ...any) {
	t.AddCell(fmt.Sprintf(s, f...))
}

func (t *TablePrinter) AddRow(str ...any) {
	for _, s := range str {
		t.AddCell(s)
	}
	t.EndRow()
}

func (t *TablePrinter) AddRowFunc(f func(int, int) string) {
	for ri := range t.TotalRows {
		row := make([]any, t.TotalRows)
		for ci := range row {
			row[ci] = f(ri, ci)
		}
		t.AddRow(row)
		t.EndRow()
	}
}

func (t *TablePrinter) EndRow() {
	t.Rows = append(t.Rows, &TableRow{Cells: make([]*TableCell, 1)})
}

// Bytes returns the []byte value of table
func (t *TablePrinter) Bytes() []byte {
	return []byte(t.String())
}

// String returns the string value of table. Alternative to Render()
func (t *TablePrinter) String() string {
	return t.Render()
}

// String returns the string representation of the row
func (r *TableRow) String() string {
	// get the max number of lines for each cell
	var lc int // line count
	for _, cell := range r.Cells {
		if clc := len(strings.Split(cell.String(), "\n")); clc > lc {
			lc = clc
		}
	}

	// allocate a two-dimensional array of cells for each line and add size them
	cells := make([][]*TableCell, lc)
	for x := range lc {
		cells[x] = make([]*TableCell, len(r.Cells))
		for y := range r.Cells {
			cells[x][y] = &TableCell{Width: r.Cells[y].Width}
		}
	}

	// insert each line in a cell as new cell in the cells array
	for y, cell := range r.Cells {
		lines := strings.Split(cell.String(), "\n")
		for x, line := range lines {
			cells[x][y].Value = line
		}
	}

	// format each line
	lines := make([]string, lc)
	for x := range lines {
		line := make([]string, len(cells[x]))
		for y := range cells[x] {
			line[y] = cells[x][y].String()
		}
		lines[x] = text.Join(line, r.Separator)
	}
	return strings.Join(lines, "\n")
}

// purgeRow removes nil cells and rows
func (t *TablePrinter) purgeRow() {
	newSlice := make([]*TableRow, 0, len(t.Rows))
	for _, row := range t.Rows {
		var newRow *TableRow
		if len(row.Cells) > 0 && row.Cells != nil {
			var newCells []*TableCell
			for _, cell := range row.Cells {
				if cell != nil {
					newCells = append(newCells, cell)
				}
			}
			newRow = &TableRow{Cells: newCells}
		}

		if newRow != nil {
			newSlice = append(newSlice, newRow)
		}
	}
	t.Rows = newSlice
}

// Render builds and returns the string representation of the table
func (t *TablePrinter) Render() string {
	if len(t.Rows) == 0 {
		return ""
	}
	// remove nil cells and rows
	t.purgeRow()

	colWidths := t.colWidths()

	var lines []string
	for _, row := range t.Rows {
		row.Separator = t.Separator()
		for i, cell := range row.Cells {
			cell.Width = colWidths[i]
			cell.widthComputed = true
			cell.Wrap = t.Wrap
		}
		lines = append(lines, row.String())
	}
	return text.Join(lines, "\n")
}

// LineWidth returns the max width of all the lines in a cell
func (c *TableCell) LineWidth() int {
	width := 0
	for s := range strings.SplitSeq(c.String(), "\n") {
		w := text.StringWidth(s)
		if w > width {
			width = w
		}
	}
	return width
}

// String returns the string formatted representation of the cell
func (c *TableCell) String() string {
	if c == nil {
		return ""
	}
	if c.Value == nil {
		return text.PadLeft(" ", c.Width, ' ')
	}

	// convert value to string
	s := fmt.Sprintf("%v", c.Value)
	// Truncate both pads short values and bounds long ones, so it aligns the cell
	// to its width. Width is only terminal derived on a TTY (colWidths skips the
	// terminal cap for a padded non-TTY), so a padded non-TTY stays lossless
	// unless an explicit MaxColWidth applies. A padded cell is formatted even at
	// a computed width of zero, a column squeezed out by a narrow terminal
	// collapses rather than printing unformatted and breaking alignment.
	if (c.Width > 0 || (c.pad && c.widthComputed)) && (c.isaTTY || c.pad) {
		if c.Wrap && c.Width > 0 && len(s) > c.Width {
			return text.WrapString(s, c.Width)
		} else {
			return text.Truncate(s, c.Width)
		}
	}
	return s
}

// colWidths determine the width for each column (cell in a row)
func (t *TablePrinter) colWidths() []int {
	var colWidths []int
	for _, row := range t.Rows {
		for i, cell := range row.Cells {
			// resize colwidth array
			if i+1 > len(colWidths) {
				colWidths = append(colWidths, 0)
			}
			cellwidth := cell.LineWidth()
			if t.MaxColWidth != 0 && cellwidth > t.MaxColWidth {
				cellwidth = t.MaxColWidth
			}

			if cellwidth > colWidths[i] {
				colWidths[i] = cellwidth
			}
		}
	}
	numCols := len(colWidths)
	separatorWidth := (numCols - 1) * len(t.Separator())
	totalWidth := separatorWidth
	for _, width := range colWidths {
		totalWidth += width
	}

	// a padded non-TTY has no terminal to fit into, so terminal width must never drive truncation there;
	// an explicit MaxColWidth still applies.
	if !(t.Pad && !t.IsTTY) && t.MaxColWidth == 0 && totalWidth > t.TerminalWidth {
		availWidth := t.TerminalWidth - colWidths[0] - separatorWidth
		// add extra space from columns that are already narrower than threshold
		for col := 1; col < numCols; col++ {
			availColWidth := availWidth / (numCols - 1)
			if extra := availColWidth - colWidths[col]; extra > 0 {
				availWidth += extra
			}
		}
		// cap all but first column to fit available terminal width
		for col := 1; col < numCols; col++ {
			availColWidth := availWidth / (numCols - 1)
			if colWidths[col] > availColWidth {
				colWidths[col] = max(availColWidth, 0)
			}
		}
		// even with the other columns bottomed out at zero, the first column
		// alone can overflow the terminal; a padded row promises to fit, so the
		// first column is shrunk to whatever width is left
		if t.Pad {
			remainingWidth := t.TerminalWidth - separatorWidth
			for col := 1; col < numCols; col++ {
				remainingWidth -= colWidths[col]
			}
			if colWidths[0] > remainingWidth {
				colWidths[0] = max(remainingWidth, 0)
			}
		}
	}

	return colWidths
}
