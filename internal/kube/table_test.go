package kube

import (
	"bytes"
	"strings"
	"testing"
)

func rowsOf(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + rows, got %q", out)
	}
	return lines[1:] // drop header
}

func TestTableSortByStringColumn(t *testing.T) {
	var buf bytes.Buffer
	tbl := NewTable(&buf, "NAME", "ZONE")
	tbl.Row("node-b", "europe-west9-c")
	tbl.Row("node-a", "europe-west9-a")
	tbl.SortBy("zone")
	if err := tbl.Flush(); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, buf.String())
	if !strings.HasPrefix(rows[0], "node-a") {
		t.Fatalf("want europe-west9-a row first, got:\n%s", buf.String())
	}
}

func TestTableSortByNumericColumn(t *testing.T) {
	var buf bytes.Buffer
	tbl := NewTable(&buf, "NODE", "PODS")
	tbl.Row("a", "2")
	tbl.Row("b", "10")
	tbl.SortBy("pods")
	if err := tbl.Flush(); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, buf.String())
	// Numeric, not lexical: 2 before 10.
	if !strings.HasPrefix(rows[0], "a") {
		t.Fatalf("want 2 before 10 (numeric), got:\n%s", buf.String())
	}
}

func TestTableSortNoColumnKeepsOrder(t *testing.T) {
	var buf bytes.Buffer
	tbl := NewTable(&buf, "NAME")
	tbl.Row("z")
	tbl.Row("a")
	tbl.SortBy("")
	if err := tbl.Flush(); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, buf.String())
	if rows[0] != "z" {
		t.Fatalf("want insertion order preserved, got:\n%s", buf.String())
	}
}
