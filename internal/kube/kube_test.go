package kube

import (
	"bytes"
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	plain := NewPainter(Flags{})
	m := map[string]string{"a": "1", "empty": ""}
	if got := Label(plain, m, "a"); got != "1" {
		t.Errorf("got %q, want 1", got)
	}
	if got := Label(plain, m, "empty"); got != "<none>" {
		t.Errorf("got %q, want <none>", got)
	}
	if got := Label(plain, m, "missing"); got != "<none>" {
		t.Errorf("got %q, want <none>", got)
	}
	// With color on, the placeholder is muted (gray); a present value is not.
	color := NewPainter(Flags{Color: true})
	if got := Label(color, m, "missing"); got != "\x1b[90m<none>\x1b[0m" {
		t.Errorf("missing label not muted: %q", got)
	}
	if got := Label(color, m, "a"); got != "1" {
		t.Errorf("present label should be uncolored: %q", got)
	}
}

func TestNamespaceScope(t *testing.T) {
	if got := (Flags{Namespace: "foo"}).NamespaceScope(); got != "foo" {
		t.Errorf("got %q, want foo", got)
	}
	if got := (Flags{Namespace: "foo", AllNamespaces: true}).NamespaceScope(); got != "" {
		t.Errorf("got %q, want empty (all)", got)
	}
	if got := (Flags{}).NamespaceScope(); got != "" {
		t.Errorf("got %q, want empty (all)", got)
	}
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	tw := NewTable(&buf, NewPainter(Flags{}), "NAME", "VAL")
	tw.Row("a", "1")
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "a") {
		t.Fatalf("unexpected table:\n%s", out)
	}
}
