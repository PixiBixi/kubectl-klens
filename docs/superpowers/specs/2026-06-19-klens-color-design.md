# kubectl-klens — colorized output design

Date: 2026-06-19

## Motivation

`kubectl-klens` emits plain, uncolored tables. Users who alias `kubectl=kubecolor`
get colorized output for native kubectl verbs but **not** for `kubectl klens`:
kubecolor only colorizes formats it recognizes (`get`, `describe`, ...) and
passes plugin output through verbatim. It cannot understand klens' bespoke
tables.

The fix is therefore **not** "make klens compatible with kubecolor" — it is to
give klens its own ANSI colors. Codes emitted by klens survive kubecolor's
passthrough, so the result works both standalone and under the kubecolor alias.

## Goals

- Semantic color across all klens tables: green = good, yellow = warning,
  red = bad, gray = muted placeholder, bold = headers.
- Standard, predictable control: `--color=auto|always|never`, `NO_COLOR`,
  `KLENS_COLOR`.
- No behavior change when color is disabled (the default in non-TTY / piped /
  test contexts). Existing substring-based tests stay green.

## Non-goals

- Theming / configurable palettes. Fixed palette only.
- Coloring the interactive `secret` picker output or decoded secret values.
- 24-bit/256-color. We use the 8 basic ANSI SGR colors for broad terminal
  support.

## Control surface

- Flag `--color=auto|always|never`, default `auto`.
  - `auto` → color when stdout is a TTY (reuse the `isTTY` logic in `secret.go`).
  - `always` → always color (the kubecolor case: stdout is a pipe under the
    alias, so `auto` would disable; `always` forces it on).
  - `never` → never color.
- `NO_COLOR` env (any non-empty value) disables color.
- `KLENS_COLOR=auto|always|never` env, for set-and-forget (e.g. a user who
  always runs through the kubecolor alias sets `KLENS_COLOR=always` once).

### Resolution order (highest precedence first)

1. `--color` flag, if explicitly passed.
2. `KLENS_COLOR` env, if set.
3. `NO_COLOR` env → forces disabled.
4. TTY detection (the `auto` default).

This collapses to a single resolved `bool`, stored on `kube.Flags.Color`.
`--color=always` overrides `NO_COLOR` only when the flag is explicitly passed
(explicit user intent wins); the bare `auto` default does not.

## Components

### `internal/cli` — color resolution

The dispatcher resolves the color `bool` once (flag + env + TTY of the `Out`
writer) and sets `kube.Flags.Color`. `--color` is a global flag registered in
the existing `globalFlags` table (so it appears in `--help` and FlagSet
registration without drift). Completion offers `auto|always|never` for
`--color`.

### `internal/kube` — palette + painter (`color.go`)

```go
type Painter struct{ enabled bool }

func NewPainter(f Flags) Painter        // Painter{enabled: f.Color}
func (p Painter) OK(s string) string    // green
func (p Painter) Warn(s string) string  // yellow
func (p Painter) Bad(s string) string   // red
func (p Painter) Muted(s string) string // gray/dim
func (p Painter) Header(s string) string// bold
func (p Painter) Status(s string) string// classifier for unambiguous tokens
```

- When `!enabled` or `s == ""`, every method returns `s` unchanged.
- `Status` maps unambiguous tokens: `Ready/Healthy/Bound/Running → OK`,
  `Pending/Progressing → Warn`, `NotReady/CrashLoopBackOff/Lost/Error → Bad`.
  Unknown tokens pass through uncolored.
- Ambiguous booleans (node-condition pressure `True/False`, `privileged true`)
  are colored explicitly by the view (it knows the polarity), not by `Status`.
- No global mutable state: the painter is built per view from `Flags`, so
  parallel `-race` tests are safe.

### `internal/kube` — ANSI-aware `Table`

The `tabwriter`-based alignment in `Table.Flush` measures cell width in runes,
so embedded ANSI codes inflate the measured width and break alignment of any
following column (verified by spike: color in a middle column pushes the next
column out). `tabwriter`'s `Escape` mechanism leaves `\xff` bytes in the output
(also verified), so it is not usable.

`Table.Flush` is reworked to align on **visible** width:

1. Compute each column's max visible width, where visible width = rune count
   after stripping ANSI SGR sequences (regex `\x1b\[[0-9;]*m`).
2. Pad each cell with spaces to `maxVisible + gap`, where the pad count is
   `maxVisible - visibleWidth(cell) + gap` (so invisible bytes don't shift
   padding). The last column is not padded.
3. Headers are rendered through `Painter.Header`.

When color is disabled there is no ANSI in any cell, so visible width == byte
width and output is byte-identical to today. This removes the `text/tabwriter`
dependency from `Table`.

## Per-command semantic mapping

Views opt in cell by cell using the painter:

| Command | Coloring |
|---|---|
| node-conditions | READY: Ready=green / NotReady=red; pressure (mem/disk/pid): True=red, False=muted |
| restarts | RESTARTS >0 = yellow; reason CrashLoopBackOff/Error = red |
| autoscaler | summary line + HEALTH Healthy=green/else red; SCALEUP/SCALEDOWN InProgress=yellow; LAST-CHANGE muted |
| privileged | flag cells `true` (privileged/hostNetwork/hostPID/hostIPC) = red |
| pvc | STATUS Bound=green / Pending=yellow / Lost=red |
| max-pods | FREE 0 = red (node at its pod ceiling); otherwise uncolored (no arbitrary "low" threshold) |
| images / image-count | tag `latest` = yellow (anti-pattern) |
| on-node | phase/STATUS via `Status` classifier |
| no-limits / no-requests / default-sa | findings: missing-marker / default-SA cell = yellow |
| nodes, taints, capacity, zones, pods-per-node, reqlim, svc-fqdn | bold header only (no status semantics) |
| secret | unchanged (raw decoded value, no table) |

## Testing

- `bytes.Buffer` is not a TTY, so `auto` resolves to disabled in tests → every
  existing substring assertion stays green with no edits.
- New targeted tests run with `kube.Flags{Color: true}` and assert:
  - the expected ANSI codes wrap the expected tokens (e.g. READY=Ready is green
    in node-conditions, a `latest` tag is yellow in image-count);
  - alignment is preserved despite ANSI — a colored middle column does not shift
    the following column (assert column start positions on visible width);
  - `--color=never` / `NO_COLOR` produce byte-identical output to the current
    plain rendering.
- Resolution-order tests in `internal/cli`: flag vs `KLENS_COLOR` vs `NO_COLOR`
  vs TTY precedence.
- Completion test: `--color` offers `auto|always|never`.

## Rejected alternatives

- **tabwriter + `Escape` brackets** — spike showed the `\xff` escape bytes are
  emitted verbatim into the output (`M-^?` under `cat -v`). Rejected.
- **Color only the last column** — tabwriter does not pad the trailing column so
  it would align, but most klens status columns are in the middle. Rejected as
  too limiting.
- **Global color state** — simpler call sites but mutable global state is unsafe
  for the parallel `-race` test suite. Rejected in favor of a `Flags`-threaded
  `Painter`.

## Documentation

Update `README.md`: document `--color`, `NO_COLOR`, `KLENS_COLOR`, the kubecolor
note (`KLENS_COLOR=always` under the alias), and add `--color` to the Flags
list. Update `CLAUDE.md` architecture notes for the painter + ANSI-aware table.
