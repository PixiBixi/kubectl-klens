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
	tbl := NewTable(&buf, NewPainter(Flags{}), "NAME", "ZONE")
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
	tbl := NewTable(&buf, NewPainter(Flags{}), "NODE", "PODS")
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

func TestTableSortByColoredNumericColumn(t *testing.T) {
	var buf bytes.Buffer
	paint := NewPainter(Flags{Color: true})
	tbl := NewTable(&buf, paint, "NODE", "FREE")
	tbl.Row("a", paint.Bad("2"))   // red
	tbl.Row("b", paint.OK("100"))  // green
	tbl.Row("c", paint.Warn("23")) // yellow
	tbl.SortBy("free")
	if err := tbl.Flush(); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, buf.String())
	// ANSI codes must be stripped before comparing: numeric order 2 < 23 < 100,
	// not sorted by the color-code prefix (which would put red 2 then green 100).
	for i, want := range []string{"a", "c", "b"} {
		if !strings.HasPrefix(rows[i], want) {
			t.Fatalf("want numeric order a(2),c(23),b(100), got:\n%s", buf.String())
		}
	}
}

func TestTableSortNoColumnKeepsOrder(t *testing.T) {
	var buf bytes.Buffer
	tbl := NewTable(&buf, NewPainter(Flags{}), "NAME")
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

func TestTablePlainOutputUnchanged(t *testing.T) {
	var buf bytes.Buffer
	tbl := NewTable(&buf, NewPainter(Flags{}), "NAME", "STATUS", "AGE")
	tbl.Row("node-a", "Ready", "10d")
	tbl.Row("node-bbbb", "NotReady", "3d")
	if err := tbl.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "NAME       STATUS    AGE\n" +
		"node-a     Ready     10d\n" +
		"node-bbbb  NotReady  3d\n"
	if buf.String() != want {
		t.Fatalf("plain table mismatch:\ngot:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestTableColoredColumnStaysAligned(t *testing.T) {
	var buf bytes.Buffer
	paint := NewPainter(Flags{Color: true})
	tbl := NewTable(&buf, paint, "NAME", "STATUS", "AGE")
	tbl.Row("node-a", paint.OK("Ready"), "10d") // colored middle column
	tbl.Row("node-bbbb", paint.Bad("NotReady"), "3d")
	if err := tbl.Flush(); err != nil {
		t.Fatal(err)
	}
	// Strip ANSI, then the de-colored output must equal the plain layout: the
	// AGE column lines up despite the color codes.
	plain := ansiSeq.ReplaceAllString(buf.String(), "")
	want := "NAME       STATUS    AGE\n" +
		"node-a     Ready     10d\n" +
		"node-bbbb  NotReady  3d\n"
	if plain != want {
		t.Fatalf("colored table misaligned:\ngot (de-colored):\n%q\nwant:\n%q", plain, want)
	}
}

func TestTableHeaderBoldWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	tbl := NewTable(&buf, NewPainter(Flags{Color: true}), "NAME")
	tbl.Row("x")
	if err := tbl.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "\x1b[1mNAME\x1b[0m") {
		t.Fatalf("header not bold: %q", buf.String())
	}
}
