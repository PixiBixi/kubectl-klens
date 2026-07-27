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

// visibleWidthRegexp is the form visibleWidth replaced, kept as the reference
// oracle for the equivalence test below.
func visibleWidthRegexp(s string) int {
	return len([]rune(ansiSeq.ReplaceAllString(s, "")))
}

// TestVisibleWidthMatchesRegexpForm pins the hand-rolled scanner to the regexp
// it replaced, including on malformed input: cells carry values from the cluster
// (scheduler messages, secret payloads) that may contain stray escape bytes, and
// a disagreement there would silently misalign columns.
func TestVisibleWidthMatchesRegexpForm(t *testing.T) {
	cases := []string{
		"", "plain", "kube-system", "héllo wörld", "日本語",
		"\x1b[32mReady\x1b[0m",
		"\x1b[1mNAME\x1b[0m",
		"\x1b[90m<none>\x1b[0m",
		"\x1b[31mCrashLoopBackOff\x1b[0m",
		"\x1b[32ma\x1b[0m\x1b[31mb\x1b[0m",
		"\x1b[0;1;31mmulti\x1b[0m",
		// Malformed / truncated / non-SGR sequences: must count as visible text.
		"\x1b", "\x1b[", "\x1b[31", "\x1b[abcm", "\x1b[31X", "\x1b]0;title\x07",
		"a\x1b[b", "\x1b[;m", "\x1b[m",
		"trailing\x1b", "\x1b[38;5;208mext\x1b[0m",
	}
	for _, s := range cases {
		if got, want := visibleWidth(s), visibleWidthRegexp(s); got != want {
			t.Errorf("visibleWidth(%q) = %d, regexp form = %d", s, got, want)
		}
	}
}

// FuzzVisibleWidth proves the equivalence on arbitrary bytes rather than a
// hand-picked list. Run with -fuzz=FuzzVisibleWidth to explore; the seed corpus
// alone runs on every `go test`.
func FuzzVisibleWidth(f *testing.F) {
	for _, s := range []string{"", "plain", "\x1b[32mok\x1b[0m", "\x1b[", "\x1b[38;5;208mx\x1b[0m", "héllo"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := visibleWidth(s), visibleWidthRegexp(s); got != want {
			t.Fatalf("visibleWidth(%q) = %d, regexp form = %d", s, got, want)
		}
	})
}

// TestStripANSIFastPathIsFaithful checks the escape-free shortcut returns the
// input untouched, matching the regexp path.
func TestStripANSIFastPathIsFaithful(t *testing.T) {
	for _, s := range []string{"", "plain", "kube-system", "héllo", "a:b/c"} {
		if got := stripANSI(s); got != s {
			t.Errorf("stripANSI(%q) = %q, want unchanged", s, got)
		}
	}
	if got, want := stripANSI("\x1b[32mReady\x1b[0m"), "Ready"; got != want {
		t.Errorf("stripANSI = %q, want %q", got, want)
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
