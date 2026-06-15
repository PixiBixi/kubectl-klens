package kube

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Table is a thin wrapper over text/tabwriter for aligned columnar output.
type Table struct {
	w *tabwriter.Writer
}

// NewTable starts a table and writes the header row.
func NewTable(out io.Writer, headers ...string) *Table {
	w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	return &Table{w: w}
}

// Row appends one data row.
func (t *Table) Row(cols ...string) {
	fmt.Fprintln(t.w, strings.Join(cols, "\t"))
}

// Flush renders the table to the underlying writer.
func (t *Table) Flush() error {
	return t.w.Flush()
}

// Label returns the value of key in labels, or "<none>" when absent/empty.
func Label(labels map[string]string, key string) string {
	if v, ok := labels[key]; ok && v != "" {
		return v
	}
	return "<none>"
}
