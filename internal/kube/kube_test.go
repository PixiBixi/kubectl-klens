package kube

import (
	"bytes"
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	m := map[string]string{"a": "1", "empty": ""}
	if got := Label(m, "a"); got != "1" {
		t.Errorf("got %q, want 1", got)
	}
	if got := Label(m, "empty"); got != "<none>" {
		t.Errorf("got %q, want <none>", got)
	}
	if got := Label(m, "missing"); got != "<none>" {
		t.Errorf("got %q, want <none>", got)
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
	tw := NewTable(&buf, "NAME", "VAL")
	tw.Row("a", "1")
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "a") {
		t.Fatalf("unexpected table:\n%s", out)
	}
}
