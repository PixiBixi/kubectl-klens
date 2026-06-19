# klens colorized output — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `kubectl klens` native ANSI color (green/yellow/red/gray/bold) across its tables, controlled by `--color`/`NO_COLOR`/`KLENS_COLOR`, so output is readable standalone and survives kubecolor's plugin passthrough.

**Architecture:** A `kube.Painter` (built from a resolved `Flags.Color` bool) wraps cell strings in ANSI codes, no-op when disabled. `kube.Table` aligns on *visible* width (ANSI stripped) so colored middle columns don't break columns, and bolds headers via the painter. The dispatcher resolves the color bool once (flag > `KLENS_COLOR` > `NO_COLOR` > TTY) and threads it through `kube.Flags`.

**Tech Stack:** Go 1.26, `golang.org/x/term` (TTY detection, already a dep), `regexp` (ANSI stripping). No new third-party dependency.

**Spec:** `docs/superpowers/specs/2026-06-19-klens-color-design.md`

**Deviations from spec (intentional, columns don't exist):**
- `pvc` view columns are `NS POD NODE PVC` — no STATUS phase → header-only (no cell color).
- `default-sa` view columns are `NS POD` — no SA column → header-only.
These two get bold headers only, like the other non-status commands.

---

## File Structure

- **Create** `internal/kube/color.go` — `Painter`, palette constants, `Status` classifier, `visibleWidth`, `IsTTY`, `ResolveColor`. One responsibility: color decisions + ANSI helpers.
- **Create** `internal/kube/color_test.go` — painter on/off, `Status` mapping, `ResolveColor` precedence, `visibleWidth`.
- **Modify** `internal/kube/flags.go` — add `ColorMode string` (raw flag) and `Color bool` (resolved).
- **Modify** `internal/kube/table.go` — `NewTable` gains a `Painter` param; `Flush` aligns on visible width + bolds headers; drop `text/tabwriter`.
- **Modify** `internal/kube/table_test.go` — pass a disabled painter; add exact-output, ANSI-alignment, and bold-header tests.
- **Modify** `internal/view/secret.go` — use `kube.IsTTY`; thread painter into table helpers; remove local `isTTY`.
- **Modify** every other `internal/view/*.go` that builds a table — create `paint := kube.NewPainter(f)`, pass to `NewTable`, color status cells where the spec mapping applies.
- **Modify** `internal/cli/cli.go` — register/validate `--color`, resolve `Flags.Color` after parse.
- **Modify** `internal/cli/complete.go` — offer `--color` and its `auto|always|never` values.
- **Modify** `internal/cli/{cli_test.go,complete_test.go}` — reject invalid `--color`, complete color values.
- **Modify** `README.md`, `CLAUDE.md` — document the flag/env and the painter/table architecture.

**Painter variable is named `paint`, never `p`** — most views already bind `p` to a pod in their loop (`for _, p := range pods.Items`). Using `p` for the painter would shadow it.

---

## Task 1: Painter, palette, IsTTY, ResolveColor

**Files:**
- Modify: `internal/kube/flags.go`
- Create: `internal/kube/color.go`
- Test: `internal/kube/color_test.go`

- [ ] **Step 1: Add color fields to Flags**

In `internal/kube/flags.go`, add two fields to the `Flags` struct (after `Sort`):

```go
type Flags struct {
	Kubeconfig    string
	Context       string
	Namespace     string
	AllNamespaces bool
	Sort          string // command-specific sort column (e.g. image-count)
	ColorMode     string // raw --color value: "auto"|"always"|"never"|"" (unset)
	Color         bool   // resolved: whether to emit ANSI color
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/kube/color_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/kube -run 'TestPainter|TestStatus|TestVisibleWidth|TestResolveColor' -v`
Expected: FAIL — `NewPainter`, `ResolveColor`, `visibleWidth` undefined.

- [ ] **Step 4: Write the implementation**

Create `internal/kube/color.go`:

```go
package kube

import (
	"io"
	"os"
	"regexp"

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

// Status colors well-known, unambiguous status tokens. Unknown tokens (and
// ambiguous booleans like a pressure "True") are returned unchanged for the
// caller to color explicitly.
func (p Painter) Status(s string) string {
	switch s {
	case "Ready", "Healthy", "Bound", "Running", "Active", "Succeeded":
		return p.OK(s)
	case "Pending", "Progressing", "ContainerCreating":
		return p.Warn(s)
	case "NotReady", "CrashLoopBackOff", "Error", "OOMKilled", "Lost", "Failed", "Evicted":
		return p.Bad(s)
	}
	return s
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visibleWidth is the rune count of s ignoring ANSI SGR sequences, used by the
// table aligner so colored cells still line up.
func visibleWidth(s string) int {
	return len([]rune(ansiSeq.ReplaceAllString(s, "")))
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/kube -run 'TestPainter|TestStatus|TestVisibleWidth|TestResolveColor' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/kube/flags.go internal/kube/color.go internal/kube/color_test.go
git commit -m "feat(color): add Painter, status palette, and color resolution"
```

---

## Task 2: Route secret.go through kube.IsTTY

**Files:**
- Modify: `internal/view/secret.go`

- [ ] **Step 1: Replace the two isTTY call sites**

In `internal/view/secret.go`, change both `if !isTTY(out)` (lines ~45 and ~55) to `if !kube.IsTTY(out)`.

- [ ] **Step 2: Delete the local isTTY and its imports**

Remove the function at the bottom of the file:

```go
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
```

Then remove the now-unused imports `"os"` and `"golang.org/x/term"` from the import block (verify nothing else in the file uses them — currently nothing does).

- [ ] **Step 3: Run tests to verify still green**

Run: `go test ./internal/view -run TestSecret -v`
Expected: PASS (behavior unchanged; piped output path still triggers because a `bytes.Buffer` is not a TTY).

- [ ] **Step 4: Commit**

```bash
git add internal/view/secret.go
git commit -m "refactor(color): use kube.IsTTY in secret, drop the local copy"
```

---

## Task 3: ANSI-aware Table + thread painter through all views (no cell color yet)

This task changes `NewTable`'s signature, so it must update **every** caller in the same commit to keep the build green. Cell coloring is added in later tasks — here each table just gets a painter (bold headers) and visible-width alignment.

**Files:**
- Modify: `internal/kube/table.go`
- Modify: `internal/kube/table_test.go`
- Modify: all table-building views (list below)

- [ ] **Step 1: Write the failing Table tests**

In `internal/kube/table_test.go`, update the three existing `NewTable(&buf, ...)` calls to `NewTable(&buf, NewPainter(Flags{}), ...)` (disabled painter), and add:

```go
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
	tbl.Row("node-a", paint.OK("Ready"), "10d")     // colored middle column
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/kube -run TestTable -v`
Expected: FAIL — `NewTable` now takes a painter (compile error) / new asserts fail.

- [ ] **Step 3: Rewrite table.go**

Replace `internal/kube/table.go` with:

```go
package kube

import (
	"io"
	"sort"
	"strconv"
	"strings"
)

// Table buffers columnar rows and renders them aligned on visible width
// (ANSI escape codes are ignored when measuring), optionally sorted by a named
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
			a, b := cell(t.rows[i], idx), cell(t.rows[j], idx)
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
	t.writeLine(&b, t.headers, true)
	for _, r := range t.rows {
		t.writeLine(&b, r, false)
	}
	_, err := io.WriteString(t.out, b.String())
	return err
}

// writeLine renders one row, padding each column (except the last) to its
// width based on visible content, so embedded ANSI codes don't shift columns.
func (t *Table) writeLine(b *strings.Builder, cells []string, header bool) {
	last := len(t.headers) - 1
	for i := 0; i < len(t.headers); i++ {
		c := cell(cells, i)
		if header {
			b.WriteString(t.painter.Header(c))
		} else {
			b.WriteString(c)
		}
		if i < last {
			b.WriteString(strings.Repeat(" ", widthsPad(t, i, c)))
		}
	}
	b.WriteByte('\n')
}

// widthsPad is the number of spaces to add after cell c in column i.
func widthsPad(t *Table, i int, c string) int {
	// recomputed lazily to avoid threading the widths slice through writeLine
	w := visibleWidth(c)
	max := visibleWidth(t.headers[i])
	for _, r := range t.rows {
		if cw := visibleWidth(cell(r, i)); cw > max {
			max = cw
		}
	}
	return max - w + tableGap
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
```

> Note: `widthsPad` recomputes the column max per cell. Tables here are small (≤ node/pod counts), so this is fine and keeps `writeLine` simple. If a profiler ever flags it, precompute `widths` once and pass the slice — but YAGNI for now.

- [ ] **Step 4: Update every NewTable caller**

The compiler will list them. Each becomes `kube.NewTable(out, paint, <headers...>)` where `paint := kube.NewPainter(f)` is added at the top of the function. Exact list and the line to add:

- `internal/view/nodes.go` — add `paint := kube.NewPainter(f)`; `kube.NewTable(out, paint, ...)`.
- `internal/view/taints.go` — same.
- `internal/view/capacity.go` — same.
- `internal/view/zones.go` — same.
- `internal/view/podspernode.go` — same.
- `internal/view/maxpods.go` — same.
- `internal/view/nodeconditions.go` — same.
- `internal/view/reqlim.go` — same.
- `internal/view/nolimits.go` — in `reportMissing`, add `paint := kube.NewPainter(f)`; `kube.NewTable(out, paint, ...)`.
- `internal/view/images.go` — same.
- `internal/view/imagecount.go` — same.
- `internal/view/onnode.go` — same.
- `internal/view/restarts.go` — same.
- `internal/view/pvc.go` — same.
- `internal/view/defaultsa.go` — same.
- `internal/view/privileged.go` — same.
- `internal/view/svcfqdn.go` — same.
- `internal/view/autoscaler.go` — see Step 5 (painter must be threaded through `renderAutoscalerStatus`).
- `internal/view/secret.go` — `listSecrets` (has `f`): add `paint := kube.NewPainter(f)`; for `emitKeys`/`emitValue` see Step 6.

For the straightforward views, the edit is exactly two lines. Example for `internal/view/nodeconditions.go`:

```go
func NodeConditions(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "STATUS", "MEMORY", "DISK", "PID")
	// ... unchanged ...
}
```

- [ ] **Step 5: Thread painter through autoscaler rendering**

In `internal/view/autoscaler.go`:

Change `Autoscaler` to build a painter and pass it:

```go
func Autoscaler(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	cm, err := c.CoreV1().ConfigMaps("kube-system").Get(ctx, "cluster-autoscaler-status", metav1.GetOptions{})
	if err != nil {
		return err
	}
	status, ok := cm.Data["status"]
	if !ok {
		return fmt.Errorf("configmap cluster-autoscaler-status has no \"status\" field")
	}
	renderAutoscalerStatus(status, f.Sort, kube.NewPainter(f), out)
	return nil
}
```

Change `renderAutoscalerStatus` signature and its summary/table calls:

```go
func renderAutoscalerStatus(status, sortCol string, paint kube.Painter, out io.Writer) {
	cw, groups, ok := parseAutoscalerStatus(status)
	if !ok {
		fmt.Fprintln(out, status)
		return
	}
	fmt.Fprintln(out, clusterWideSummary(cw, paint))
	if len(groups) == 0 {
		return
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].lastChange != groups[j].lastChange {
			return groups[i].lastChange > groups[j].lastChange
		}
		return groups[i].name < groups[j].name
	})
	fmt.Fprintln(out)
	t := kube.NewTable(out, paint, "NODEGROUP", "HEALTH", "READY", "TARGET", "MIN", "MAX", "SCALEUP", "SCALEDOWN", "LAST-CHANGE")
	for _, g := range groups {
		t.Row(g.name, dash(g.health), dash(g.ready), dash(g.target), dash(g.min), dash(g.max), dash(g.scaleUp), dash(g.scaleDown), dash(g.lastChange))
	}
	t.SortBy(sortCol)
	t.Flush()
}
```

And update `clusterWideSummary` to accept the painter (no coloring yet — added in Task 7; for now just take the param and ignore it to keep the build green):

```go
func clusterWideSummary(cw caClusterWide, paint kube.Painter) string {
	_ = paint // colored in Task 7
	// ... existing body unchanged ...
}
```

- [ ] **Step 6: Thread painter through secret table helpers**

In `internal/view/secret.go`, change the helper signatures to take a painter and pass it from `Secret`:

```go
// in Secret(), build once and pass to the value/key emitters:
paint := kube.NewPainter(f)
// case len(args) >= 2:
return emitValue(out, paint, s, args[1])
// case len(args) == 1 ...:
return emitValue(out, paint, s, key)   // both call sites
return emitKeys(out, paint, s)
// default piped:
return listSecrets(ctx, c, f, out)     // builds its own paint inside
```

Update the helpers:

```go
func listSecrets(ctx context.Context, c kubernetes.Interface, f kube.Flags, out io.Writer) error {
	list, err := c.CoreV1().Secrets(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	sortSecrets(list.Items)
	t := kube.NewTable(out, kube.NewPainter(f), "NS", "NAME", "TYPE", "KEYS", "AGE")
	// ... unchanged ...
}

func emitKeys(out io.Writer, paint kube.Painter, s *corev1.Secret) error {
	t := kube.NewTable(out, paint, "KEY", "BYTES")
	// ... unchanged ...
}

func emitValue(out io.Writer, paint kube.Painter, s *corev1.Secret, key string) error {
	if key == "all" {
		t := kube.NewTable(out, paint, "KEY", "VALUE")
		// ... unchanged ...
	}
	// ... unchanged ...
}
```

- [ ] **Step 7: Run the whole suite**

Run: `go build ./... && go test ./... `
Expected: build OK; all tests PASS. Existing view tests run with `Flags{}` (Color false), so output is plain and byte-identical — substring asserts unaffected.

- [ ] **Step 8: Commit**

```bash
git add internal/kube/table.go internal/kube/table_test.go internal/view
git commit -m "feat(color): align tables on visible width and bold headers via painter"
```

---

## Task 4: Wire --color flag, resolution, and completion

**Files:**
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/complete.go`
- Test: `internal/cli/cli_test.go`, `internal/cli/complete_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/cli/cli_test.go` add:

```go
func TestRunRejectsInvalidColor(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"nodes", "--color", "bogus"}); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(errw.String(), "invalid --color") {
		t.Fatalf("want invalid --color error, got %q", errw.String())
	}
}

func TestRunAcceptsColorNever(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"nodes", "--color", "never"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
}
```

In `internal/cli/complete_test.go` add:

```go
func TestCompleteOffersColorFlag(t *testing.T) {
	out := completeOut(t, "nodes", "--c")
	if !strings.Contains(out, "--color") {
		t.Errorf("want --color offered:\n%s", out)
	}
}

func TestCompleteColorValues(t *testing.T) {
	out := completeOut(t, "nodes", "--color", "")
	for _, want := range []string{"auto", "always", "never"} {
		if !strings.Contains(out, want) {
			t.Errorf("color-value completion missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/cli -run 'TestRunRejectsInvalidColor|TestRunAcceptsColorNever|TestCompleteOffersColorFlag|TestCompleteColorValues' -v`
Expected: FAIL (flag unknown / not offered).

- [ ] **Step 3: Register --color in globalFlags**

In `internal/cli/cli.go`, add an entry to the `globalFlags` slice (after the all-namespaces entry):

```go
	{"--color string", "colorize output: auto|always|never (default auto)",
		func(fs *flag.FlagSet, f *kube.Flags, h string) { fs.StringVar(&f.ColorMode, "color", "", h) }},
```

- [ ] **Step 4: Validate and resolve after Parse**

In `App.Run`, after the `--sort` validation block and before `a.NewClient(f)`, add:

```go
	switch f.ColorMode {
	case "", "auto", "always", "never":
	default:
		fmt.Fprintf(a.Err, "error: invalid --color %q (want auto|always|never)\n", f.ColorMode)
		return 1
	}
	f.Color = kube.ResolveColor(f.ColorMode, a.Out)
```

- [ ] **Step 5: Offer --color and its values in completion**

In `internal/cli/complete.go`:

Add `"--color"` to the `completionFlags` slice:

```go
var completionFlags = []string{
	"--kubeconfig", "--context", "--namespace", "-n",
	"--all-namespaces", "-A", "--color", "--version", "--help", "-h",
}
```

Add a value-completion branch in `completions`, next to the `--sort` branch:

```go
	if len(prior) > 0 && prior[len(prior)-1] == "--color" {
		return withPrefix([]string{"auto", "always", "never"}, toComplete)
	}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/cli -v`
Expected: PASS (new tests green; existing tests unaffected — `--color` defaults to "" → resolves false on the test `bytes.Buffer`).

- [ ] **Step 7: Commit**

```bash
git add internal/cli/cli.go internal/cli/complete.go internal/cli/cli_test.go internal/cli/complete_test.go
git commit -m "feat(color): add --color flag with resolution and completion"
```

---

## Task 5: Color node-conditions

**Files:**
- Modify: `internal/view/nodeconditions.go`
- Test: `internal/view/nodeconditions_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/view/nodeconditions_test.go` add (adjust the helper that builds a node if the file already has one — reuse it; the assertion is what matters):

```go
func TestNodeConditionsColor(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue},
			{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse},
		}},
	}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := NodeConditions(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[32mReady\x1b[0m") {
		t.Fatalf("Ready not green:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[31mTrue\x1b[0m") {
		t.Fatalf("memory pressure True not red:\n%s", out)
	}
}
```

(Imports needed in the test file, if not present: `bytes`, `context`, `strings`, `testing`, `corev1 "k8s.io/api/core/v1"`, `metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"`, `"k8s.io/client-go/kubernetes/fake"`, `"github.com/PixiBixi/kubectl-klens/internal/kube"`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/view -run TestNodeConditionsColor -v`
Expected: FAIL (no ANSI in output).

- [ ] **Step 3: Color the cells**

In `internal/view/nodeconditions.go`, color the STATUS cell via the classifier and the pressure cells via a local helper:

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "STATUS", "MEMORY", "DISK", "PID")
	for _, n := range nodes.Items {
		t.Row(
			n.Name,
			paint.Status(nodeStatus(n)),
			pressure(paint, conditionStatus(n, corev1.NodeMemoryPressure)),
			pressure(paint, conditionStatus(n, corev1.NodeDiskPressure)),
			pressure(paint, conditionStatus(n, corev1.NodePIDPressure)),
		)
	}
```

Add the helper at the bottom of the file:

```go
// pressure colors a node pressure condition: under pressure (True) is bad,
// no pressure (False) is muted, anything else (Unknown) is left plain.
func pressure(paint kube.Painter, status string) string {
	switch status {
	case "True":
		return paint.Bad(status)
	case "False":
		return paint.Muted(status)
	}
	return status
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run TestNodeConditions -v`
Expected: PASS (color test green; existing plain test still passes — `Flags{}` → no color).

- [ ] **Step 5: Commit**

```bash
git add internal/view/nodeconditions.go internal/view/nodeconditions_test.go
git commit -m "feat(color): color node-conditions readiness and pressure"
```

---

## Task 6: Color restarts

**Files:**
- Modify: `internal/view/restarts.go`
- Test: `internal/view/restarts_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/view/restarts_test.go` add:

```go
func TestRestartsColor(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:         "c1",
			RestartCount: 5,
			State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		}}},
	}
	c := fake.NewClientset(pod)
	var buf bytes.Buffer
	if err := Restarts(context.Background(), c, kube.Flags{Color: true, AllNamespaces: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[33m5\x1b[0m") {
		t.Fatalf("restart count not yellow:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[31mCrashLoopBackOff\x1b[0m") {
		t.Fatalf("crash reason not red:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/view -run TestRestartsColor -v`
Expected: FAIL.

- [ ] **Step 3: Color the cells**

In `internal/view/restarts.go`, after building the painter, color the RESTARTS and STATE cells (every listed row has restarts > 0, so the count is always a warning):

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "RESTARTS", "STATE")
	for _, e := range list {
		t.Row(e.ns, e.pod, e.container, paint.Warn(strconv.Itoa(int(e.restarts))), paint.Status(e.state))
	}
```

(`paint.Status` maps `CrashLoopBackOff`/`Error`/`OOMKilled` → red, `Running` → green, others unchanged.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run TestRestarts -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view/restarts.go internal/view/restarts_test.go
git commit -m "feat(color): color restart counts and crash reasons"
```

---

## Task 7: Color autoscaler

**Files:**
- Modify: `internal/view/autoscaler.go`
- Test: `internal/view/autoscaler_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/view/autoscaler_test.go` add a colored render helper and test:

```go
func TestAutoscalerColor(t *testing.T) {
	c := fake.NewClientset(autoscalerCM(yamlStatus))
	var buf bytes.Buffer
	if err := Autoscaler(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Cluster-wide health green in the summary line.
	if !strings.Contains(out, "\x1b[32mHealthy\x1b[0m") {
		t.Fatalf("cluster-wide Healthy not green:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/view -run TestAutoscalerColor -v`
Expected: FAIL (summary not colored — `clusterWideSummary` still ignores the painter from Task 3).

- [ ] **Step 3: Color the summary and table cells**

In `internal/view/autoscaler.go`, implement coloring in `clusterWideSummary` (replace the `_ = paint` stub). **Read the existing function body first** and keep its exact label/spacing format — only wrap the health token with `health(paint, …)` and each scale-state token with `scaleState(paint, …)`. The block below shows the intended shape; reconcile it with the real field names and formatting rather than overwriting blindly (a format drift would break the existing plain-output autoscaler test):

```go
func clusterWideSummary(cw caClusterWide, paint kube.Painter) string {
	var b strings.Builder
	b.WriteString("Cluster-wide: ")
	if cw.health != "" {
		b.WriteString(health(paint, cw.health))
	} else {
		b.WriteString("Unknown")
	}
	if cw.scaleUp != "" {
		b.WriteString("  scaleUp=" + scaleState(paint, cw.scaleUp))
	}
	if cw.scaleDown != "" {
		b.WriteString("  scaleDown=" + scaleState(paint, cw.scaleDown))
	}
	if cw.ready != "" {
		registered := cw.registered
		if registered == "" {
			registered = cw.ready
		}
		fmt.Fprintf(&b, "  (ready %s/%s)", cw.ready, registered)
	}
	if cw.timestamp != "" {
		b.WriteString("  @ " + cw.timestamp)
	}
	return b.String()
}
```

Color the table cells in `renderAutoscalerStatus` (the `t.Row(...)` from Task 3 becomes):

```go
	for _, g := range groups {
		t.Row(g.name, health(paint, dash(g.health)), dash(g.ready), dash(g.target), dash(g.min), dash(g.max),
			scaleState(paint, dash(g.scaleUp)), scaleState(paint, dash(g.scaleDown)), paint.Muted(dash(g.lastChange)))
	}
```

Add the two helpers at the bottom of the file:

```go
// health colors a cluster-autoscaler health value: Healthy is good, anything
// else non-empty (e.g. Unhealthy) is bad. "-" and "" pass through.
func health(paint kube.Painter, status string) string {
	switch status {
	case "Healthy":
		return paint.OK(status)
	case "", "-":
		return status
	}
	return paint.Bad(status)
}

// scaleState colors a scale-up/scale-down state: in-progress activity is a
// warning; other states (NoActivity, NoCandidates, CandidatesPresent) are left
// plain.
func scaleState(paint kube.Painter, status string) string {
	if status == "InProgress" {
		return paint.Warn(status)
	}
	return status
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run TestAutoscaler -v`
Expected: PASS — `TestAutoscalerColor` green; the existing format/sort tests still pass because they run with `Flags{}`/`Flags{Sort:...}` (Color false), so cells stay plain and `rowFields` still sees `Healthy`, the bare timestamp, etc.

- [ ] **Step 5: Commit**

```bash
git add internal/view/autoscaler.go internal/view/autoscaler_test.go
git commit -m "feat(color): color autoscaler health and scale states"
```

---

## Task 8: Color privileged

**Files:**
- Modify: `internal/view/privileged.go`
- Test: `internal/view/privileged_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/view/privileged_test.go` add:

```go
func TestPrivilegedColor(t *testing.T) {
	tru := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:            "c1",
			SecurityContext: &corev1.SecurityContext{Privileged: &tru},
		}}},
	}
	c := fake.NewClientset(pod)
	var buf bytes.Buffer
	if err := Privileged(context.Background(), c, kube.Flags{Color: true, AllNamespaces: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[31mprivileged\x1b[0m") {
		t.Fatalf("flags not red:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/view -run TestPrivilegedColor -v`
Expected: FAIL.

- [ ] **Step 3: Color the FLAGS cell**

In `internal/view/privileged.go`:

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "FLAGS")
	for _, p := range pods.Items {
		podFlags := podSecurityFlags(p)
		for _, ctr := range p.Spec.Containers {
			flags := append(containerSecurityFlags(ctr, p), podFlags...)
			if len(flags) > 0 {
				t.Row(p.Namespace, p.Name, ctr.Name, paint.Bad(strings.Join(flags, ",")))
			}
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run TestPrivileged -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view/privileged.go internal/view/privileged_test.go
git commit -m "feat(color): flag privileged/host security findings in red"
```

---

## Task 9: Color max-pods (saturated nodes)

**Files:**
- Modify: `internal/view/maxpods.go`
- Test: `internal/view/maxpods_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/view/maxpods_test.go` add a test where a node is at its ceiling (FREE == 0):

```go
func TestMaxPodsColorWhenFull(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status:     corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("1")}},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"}, Spec: corev1.PodSpec{NodeName: "node-a"}}
	c := fake.NewClientset(node, pod)
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[31m0\x1b[0m") {
		t.Fatalf("zero free slots not red:\n%s", buf.String())
	}
}
```

(Needs import `"k8s.io/apimachinery/pkg/api/resource"` in the test file.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/view -run TestMaxPodsColorWhenFull -v`
Expected: FAIL.

- [ ] **Step 3: Color FREE == 0**

In `internal/view/maxpods.go`, build the painter and color the free cell when the node is full:

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NODE", "MAXPODS", "USED", "FREE")
	for _, n := range nodes.Items {
		u := used[n.Name]
		maxCell, freeCell := "none", "none"
		if q, ok := n.Status.Allocatable[corev1.ResourcePods]; ok {
			max := int(q.Value())
			maxCell = strconv.Itoa(max)
			freeCell = strconv.Itoa(max - u)
		}
		if freeCell == "0" {
			freeCell = paint.Bad(freeCell)
		}
		t.Row(n.Name, maxCell, strconv.Itoa(u), freeCell)
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run TestMaxPods -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view/maxpods.go internal/view/maxpods_test.go
git commit -m "feat(color): mark nodes at their pod ceiling in red"
```

---

## Task 10: Color images and image-count (latest tag)

**Files:**
- Modify: `internal/view/images.go`, `internal/view/imagecount.go`
- Test: `internal/view/images_test.go`, `internal/view/imagecount_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/view/imagecount_test.go` add:

```go
func TestImageCountColorLatest(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1", Image: "nginx:latest"}}},
	}
	c := fake.NewClientset(pod)
	var buf bytes.Buffer
	if err := ImageCount(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[33mlatest\x1b[0m") {
		t.Fatalf("latest tag not yellow:\n%s", buf.String())
	}
}
```

In `internal/view/images_test.go` add:

```go
func TestImagesColorLatest(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1", Image: "nginx"}}}, // implicit :latest
	}
	c := fake.NewClientset(pod)
	var buf bytes.Buffer
	if err := Images(context.Background(), c, kube.Flags{Color: true, AllNamespaces: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[33mlatest\x1b[0m") {
		t.Fatalf("latest tag not yellow:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/view -run 'TestImageCountColorLatest|TestImagesColorLatest' -v`
Expected: FAIL.

- [ ] **Step 3: Color the latest tag in both views**

In `internal/view/imagecount.go`:

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "COUNT", "REGISTRY", "IMAGE", "TAG")
	for _, e := range list {
		t.Row(strconv.Itoa(e.n), e.registry, e.repo, latestTag(paint, e.tag))
	}
```

In `internal/view/images.go`:

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "PODNAME", "CONTAINER", "PULL", "IMAGE", "TAG")
	for _, p := range pods.Items {
		for _, ctr := range p.Spec.Containers {
			image, tag := splitImageTag(ctr.Image)
			t.Row(p.Name, ctr.Name, string(ctr.ImagePullPolicy), image, latestTag(paint, tag))
		}
	}
```

Add the shared helper once, in `internal/view/images.go` (both files are in package `view`):

```go
// latestTag highlights a floating "latest" tag, an operational anti-pattern.
func latestTag(paint kube.Painter, tag string) string {
	if tag == "latest" {
		return paint.Warn(tag)
	}
	return tag
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run 'TestImage' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view/images.go internal/view/imagecount.go internal/view/images_test.go internal/view/imagecount_test.go
git commit -m "feat(color): highlight floating latest tags in yellow"
```

---

## Task 11: Color on-node (pod phase)

**Files:**
- Modify: `internal/view/onnode.go`
- Test: `internal/view/onnode_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/view/onnode_test.go` add:

```go
func TestOnNodeColor(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	c := fake.NewClientset(pod)
	var buf bytes.Buffer
	if err := OnNode(context.Background(), c, kube.Flags{Color: true, AllNamespaces: true}, []string{"node-a"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[32mRunning\x1b[0m") {
		t.Fatalf("Running phase not green:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/view -run TestOnNodeColor -v`
Expected: FAIL.

- [ ] **Step 3: Color the STATUS cell**

In `internal/view/onnode.go`:

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "STATUS", "NODE")
	for _, p := range pods.Items {
		if p.Spec.NodeName != node {
			continue // defensive: fake clientset ignores FieldSelector
		}
		t.Row(p.Namespace, p.Name, paint.Status(string(p.Status.Phase)), p.Spec.NodeName)
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run TestOnNode -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view/onnode.go internal/view/onnode_test.go
git commit -m "feat(color): color pod phase in on-node"
```

---

## Task 12: Color no-limits / no-requests (MISSING)

**Files:**
- Modify: `internal/view/nolimits.go`
- Test: `internal/view/nolimits_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/view/nolimits_test.go` add:

```go
func TestNoLimitsColor(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "team-a"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}}, // no limits at all
	}
	c := fake.NewClientset(pod)
	var buf bytes.Buffer
	if err := NoLimits(context.Background(), c, kube.Flags{Color: true, AllNamespaces: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[33mcpu,memory\x1b[0m") {
		t.Fatalf("missing resources not yellow:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/view -run TestNoLimitsColor -v`
Expected: FAIL.

- [ ] **Step 3: Color the MISSING cell in the shared helper**

In `internal/view/nolimits.go`, `reportMissing` (already has `paint` from Task 3):

```go
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "MISSING")
	for _, p := range pods.Items {
		if p.Namespace == "kube-system" {
			continue
		}
		for _, ctr := range p.Spec.Containers {
			if m := missingResources(pick(ctr)); m != "" {
				t.Row(p.Namespace, p.Name, ctr.Name, paint.Warn(m))
			}
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/view -run 'TestNoLimits|TestNoRequests' -v`
Expected: PASS (both share `reportMissing`).

- [ ] **Step 5: Commit**

```bash
git add internal/view/nolimits.go internal/view/nolimits_test.go
git commit -m "feat(color): highlight missing limits/requests in yellow"
```

---

## Task 13: Documentation + final verification

**Files:**
- Modify: `README.md`, `CLAUDE.md`

- [ ] **Step 1: Update README.md**

Add `--color` to the Flags line (currently around line 70-71):

```
Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`,
`--color`, `--version`.
```

Add a short Color section after the Usage section:

```markdown
## Color

klens colorizes its tables: green = good (Ready/Healthy/Bound/Running),
yellow = warning (Pending, high restart counts, floating `latest` tags),
red = bad (NotReady/CrashLoopBackOff, node pressure, privileged flags, a node
at its pod ceiling), gray = muted placeholders, bold = headers.

Control it with `--color=auto|always|never` (default `auto`, which colors only
when stdout is a terminal). `NO_COLOR` disables color; `KLENS_COLOR` sets the
default via the environment.

Under kubecolor (`alias kubectl=kubecolor`) klens' stdout is a pipe, so `auto`
turns color off. kubecolor passes plugin output through unchanged, so klens'
own colors survive — force them on with `--color=always` or, once in your
shell, `export KLENS_COLOR=always`.
```

- [ ] **Step 2: Update CLAUDE.md architecture notes**

In the `internal/kube` bullet, note the painter and visible-width table:

```
- **`internal/kube`** — ... the `Table` helper used for all columnar output, and
  `color.go` (`Painter` + `ResolveColor` + `IsTTY`). `Table` aligns on *visible*
  width (ANSI stripped) so colored cells don't break columns, and bolds headers
  via the `Painter` passed to `NewTable`. Color is resolved once in the
  dispatcher (`--color` > `KLENS_COLOR` > `NO_COLOR` > TTY) into `Flags.Color`.
```

In the "Adding a subcommand" section, add a note:

```
Views color cells by building `paint := kube.NewPainter(f)` and wrapping status
cells (`paint.OK/Warn/Bad/Muted` or the `paint.Status` classifier); pass `paint`
to `kube.NewTable`. Painter is named `paint`, not `p`, to avoid shadowing the
`p` pod loop variable.
```

- [ ] **Step 3: Full verification**

Run: `go build ./... && go vet ./... && staticcheck ./... && go test -race ./...`
Expected: build OK, vet clean, staticcheck clean, all tests PASS.

- [ ] **Step 4: Manual smoke test (color visible)**

Run: `go run . --color=always node-conditions 2>/dev/null | cat -v | head` against a real context if available, or trust the unit tests. Confirm ANSI codes appear and columns align.

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs(color): document --color/NO_COLOR/KLENS_COLOR and painter architecture"
```

---

## Notes for the implementer

- **Color is off in tests by default.** Every existing view test passes `kube.Flags{}` (or `{Sort:...}`), so `Color` is false, the painter is a no-op, and output stays byte-identical. Do not add color assertions to existing tests — add new `...Color` tests.
- **Painter variable name is `paint`.** Never `p` (shadows the pod loop variable in most views).
- **The Table always strips ANSI for width**, regardless of whether color is enabled, so a disabled-painter table is still correct (no ANSI present → no-op strip).
- **Last column is never padded** — preserves the no-trailing-whitespace behavior of the old tabwriter output.
