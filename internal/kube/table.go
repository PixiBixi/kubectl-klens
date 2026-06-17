package kube

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Table buffers columnar rows and renders them aligned via text/tabwriter,
// optionally sorted by a named column.
type Table struct {
	out     io.Writer
	headers []string
	rows    [][]string
	sortCol string
}

// NewTable starts a table with the given header row.
func NewTable(out io.Writer, headers ...string) *Table {
	return &Table{out: out, headers: headers}
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

// Flush renders the table, applying the sort column if one was set.
func (t *Table) Flush() error {
	if idx := t.columnIndex(t.sortCol); idx >= 0 {
		numeric := columnIsNumeric(t.rows, idx)
		sort.SliceStable(t.rows, func(i, j int) bool {
			a, b := cell(t.rows[i], idx), cell(t.rows[j], idx)
			if numeric {
				af, _ := strconv.ParseFloat(a, 64)
				bf, _ := strconv.ParseFloat(b, 64)
				return af < bf
			}
			return a < b
		})
	}
	w := tabwriter.NewWriter(t.out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(t.headers, "\t"))
	for _, r := range t.rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	return w.Flush()
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
		if _, err := strconv.ParseFloat(cell(r, idx), 64); err != nil {
			return false
		}
	}
	return true
}

// Label returns the value of key in labels, or "<none>" when absent/empty.
func Label(labels map[string]string, key string) string {
	if v, ok := labels[key]; ok && v != "" {
		return v
	}
	return "<none>"
}
