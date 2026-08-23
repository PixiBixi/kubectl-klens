package kube

import (
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// ANSI SGR codes used for klens output. Basic 8-color set for broad terminal
// support.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiGray   = "\x1b[90m"
)

// Painter wraps strings in ANSI color when enabled; every method is a no-op
// (returns its input unchanged) when color is disabled or the string is empty.
type Painter struct{ enabled bool }

// NewPainter builds a Painter from the resolved Flags.Color bool.
func NewPainter(f Flags) Painter { return Painter{enabled: f.Color} }

func (p Painter) paint(code, s string) string {
	if !p.enabled || s == "" {
		return s
	}
	return code + s + ansiReset
}

func (p Painter) OK(s string) string     { return p.paint(ansiGreen, s) }
func (p Painter) Warn(s string) string   { return p.paint(ansiYellow, s) }
func (p Painter) Bad(s string) string    { return p.paint(ansiRed, s) }
func (p Painter) Muted(s string) string  { return p.paint(ansiGray, s) }
func (p Painter) Header(s string) string { return p.paint(ansiBold, s) }

// Status colors well-known status tokens. "Unknown" is treated as bad (a lost
// node/pod/container is a real problem). Tokens not listed (and ambiguous
// booleans like a pressure "True") are returned unchanged for the caller to
// color explicitly.
func (p Painter) Status(s string) string {
	switch s {
	case "Ready", "Healthy", "Bound", "Running", "Active", "Succeeded":
		return p.OK(s)
	case "Pending", "Progressing", "ContainerCreating", "PodInitializing":
		return p.Warn(s)
	case "NotReady", "CrashLoopBackOff", "Error", "OOMKilled", "Lost", "Failed", "Evicted", "Unknown",
		"Unschedulable", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "InvalidImageName":
		return p.Bad(s)
	}
	return s
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI SGR sequences, leaving the visible text. Used when
// sorting so colored cells compare by their value. The escape-free fast path
// matters: ReplaceAllString copies the whole string even when nothing matches,
// and most cells carry no color at all.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, ansiEscape) {
		return s
	}
	return ansiSeq.ReplaceAllString(s, "")
}

const ansiEscape = '\x1b'

// visibleWidth is the rune count of s ignoring ANSI SGR sequences, used by the
// table aligner so colored cells still line up. It scans in a single pass rather
// than going through stripANSI: the table measures every cell at least twice, so
// this runs on the hot path of every command's output. Benchmarked at ~10x the
// regexp form and allocation-free (see BenchmarkVisibleWidth).
func visibleWidth(s string) int {
	if !strings.ContainsRune(s, ansiEscape) {
		return utf8.RuneCountInString(s)
	}
	n := 0
	for i := 0; i < len(s); {
		// Recognize exactly what ansiSeq matches - ESC '[' [0-9;]* 'm' - so a
		// truncated or malformed sequence is counted as visible text rather than
		// silently swallowed. Cell values are not all ours (scheduler messages
		// and secret values reach the table verbatim), so the two forms must
		// agree on arbitrary input; TestVisibleWidthMatchesRegexpForm checks it.
		if s[i] == ansiEscape && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				i = j + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// IsTTY reports whether w is a terminal (an *os.File on a tty).
func IsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// ResolveColor decides whether to emit color. Precedence: an explicit --color
// flag wins; then KLENS_COLOR; then NO_COLOR (disables); else TTY detection.
// Unrecognized flag/env values fall through to the NO_COLOR/TTY checks.
func ResolveColor(flagMode string, out io.Writer) bool {
	mode := flagMode
	if mode == "" {
		mode = os.Getenv("KLENS_COLOR")
	}
	switch mode {
	case "always":
		return true
	case "never":
		return false
	case "auto":
		return IsTTY(out)
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return IsTTY(out)
}
