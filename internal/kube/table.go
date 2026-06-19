package kube

import (
	"io"
	"sort"
	"strconv"
	"strings"
)

// Table buffers columnar rows and renders them aligned on visible width (ANSI
// escape codes are ignored when measuring), optionally sorted by a named
// column. Headers are bolded when the painter is enabled.
type Table struct {
	out     io.Writer
	painter Painter
	headers []string
	rows    [][]string
	sortCol string
}

// NewTable starts a table with the given painter and header row.
func NewTable(out io.Writer, p Painter, headers ...string) *Table {
	return &Table{out: out, painter: p, headers: headers}
}

// Row appends one data row.
func (t *Table) Row(cols ...string) {
	row := make([]string, len(cols))
	copy(row, cols)
	t.rows = append(t.rows, row)
}

// SortBy sorts rows ascending by the named column (case-insensitive match
// against the headers), detecting numeric columns so counts order naturally.
// An empty column name or one absent from the headers is a no-op.
func (t *Table) SortBy(column string) {
	t.sortCol = column
}

const tableGap = 2

// Flush renders the table, applying the sort column if one was set. Columns are
// padded to their widest visible cell plus a fixed gap; the last column is not
// padded (no trailing whitespace).
func (t *Table) Flush() error {
	if idx := t.columnIndex(t.sortCol); idx >= 0 {
		numeric := columnIsNumeric(t.rows, idx)
		sort.SliceStable(t.rows, func(i, j int) bool {
			a, b := stripANSI(cell(t.rows[i], idx)), stripANSI(cell(t.rows[j], idx))
			if numeric {
				af, _ := strconv.ParseFloat(a, 64)
				bf, _ := strconv.ParseFloat(b, 64)
				return af < bf
			}
			return a < b
		})
	}
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = visibleWidth(h)
	}
	for _, r := range t.rows {
		for i := 0; i < len(widths) && i < len(r); i++ {
			if w := visibleWidth(r[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var b strings.Builder
	t.writeLine(&b, widths, t.headers, true)
	for _, r := range t.rows {
		t.writeLine(&b, widths, r, false)
	}
	_, err := io.WriteString(t.out, b.String())
	return err
}

// writeLine renders one row, padding each column (except the last) to its width
// based on visible content, so embedded ANSI codes don't shift columns.
func (t *Table) writeLine(b *strings.Builder, widths []int, cells []string, header bool) {
	last := len(t.headers) - 1
	for i := 0; i < len(t.headers); i++ {
		c := cell(cells, i)
		if header {
			c = t.painter.Header(c)
		}
		b.WriteString(c)
		if i < last {
			b.WriteString(strings.Repeat(" ", widths[i]-visibleWidth(c)+tableGap))
		}
	}
	b.WriteByte('\n')
}

func (t *Table) columnIndex(column string) int {
	if column == "" {
		return -1
	}
	for i, h := range t.headers {
		if strings.EqualFold(h, column) {
			return i
		}
	}
	return -1
}

func cell(row []string, idx int) string {
	if idx < len(row) {
		return row[idx]
	}
	return ""
}

// columnIsNumeric reports whether every cell in the column parses as a number.
func columnIsNumeric(rows [][]string, idx int) bool {
	if len(rows) == 0 {
		return false
	}
	for _, r := range rows {
		if _, err := strconv.ParseFloat(stripANSI(cell(r, idx)), 64); err != nil {
			return false
		}
	}
	return true
}

// Label returns the value of key in labels, or a muted "<none>" when
// absent/empty.
func Label(p Painter, labels map[string]string, key string) string {
	if v, ok := labels[key]; ok && v != "" {
		return v
	}
	return p.Muted("<none>")
}
