package kube

import (
	"bytes"
	"strings"
	"testing"
)

func TestPainterDisabledIsNoOp(t *testing.T) {
	paint := NewPainter(Flags{Color: false})
	for _, got := range []string{paint.OK("Ready"), paint.Bad("x"), paint.Header("H"), paint.Muted("-")} {
		if strings.Contains(got, "\x1b[") {
			t.Fatalf("disabled painter emitted ANSI: %q", got)
		}
	}
}

func TestPainterEnabledWraps(t *testing.T) {
	paint := NewPainter(Flags{Color: true})
	if got := paint.OK("Ready"); got != "\x1b[32mReady\x1b[0m" {
		t.Fatalf("OK = %q", got)
	}
	if got := paint.OK(""); got != "" {
		t.Fatalf("empty string must stay empty, got %q", got)
	}
}

func TestStatusClassifier(t *testing.T) {
	paint := NewPainter(Flags{Color: true})
	cases := map[string]string{
		"Ready":            "\x1b[32m", // green
		"Pending":          "\x1b[33m", // yellow
		"CrashLoopBackOff": "\x1b[31m", // red
		"Whatever":         "",         // unknown: unchanged
	}
	for in, wantPrefix := range cases {
		got := paint.Status(in)
		if wantPrefix == "" {
			if got != in {
				t.Fatalf("Status(%q) = %q, want unchanged", in, got)
			}
			continue
		}
		if !strings.HasPrefix(got, wantPrefix) {
			t.Fatalf("Status(%q) = %q, want prefix %q", in, got, wantPrefix)
		}
	}
}

func TestVisibleWidthIgnoresANSI(t *testing.T) {
	if w := visibleWidth("\x1b[32mReady\x1b[0m"); w != 5 {
		t.Fatalf("visibleWidth = %d, want 5", w)
	}
}

func TestResolveColor(t *testing.T) {
	var buf bytes.Buffer // not a TTY → auto resolves false
	t.Setenv("NO_COLOR", "")
	t.Setenv("KLENS_COLOR", "")

	if !ResolveColor("always", &buf) {
		t.Fatal("--color=always must enable")
	}
	if ResolveColor("never", &buf) {
		t.Fatal("--color=never must disable")
	}
	if ResolveColor("auto", &buf) {
		t.Fatal("auto on a non-TTY must disable")
	}
	// explicit always beats NO_COLOR
	t.Setenv("NO_COLOR", "1")
	if !ResolveColor("always", &buf) {
		t.Fatal("--color=always must beat NO_COLOR")
	}
	if ResolveColor("", &buf) {
		t.Fatal("NO_COLOR must disable when flag unset")
	}
	// KLENS_COLOR=always beats NO_COLOR when flag unset
	t.Setenv("KLENS_COLOR", "always")
	if !ResolveColor("", &buf) {
		t.Fatal("KLENS_COLOR=always must enable over NO_COLOR")
	}
}
