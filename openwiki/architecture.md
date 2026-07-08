# Architecture

kubectl-klens is three packages under `internal/`, layered **cli → view → kube**.
Nothing in `view` or `kube` imports `cli`; `cli` orchestrates the other two.

```
main.go                 wires BuildInfo + os streams → cli.NewApp → App.Run
  └─ internal/cli       the dispatcher: registry, flag parsing, namespace defaulting, completion
       └─ internal/view one file per subcommand (a RunFunc) + shared node helpers
            └─ internal/kube  kubeconfig plumbing, Flags, the Table renderer, the color Painter
```

## The dispatcher (`internal/cli`)

`App` holds injected `NewClient` and `Namespace` functions so `Run` is testable
without a real cluster; `NewApp` wires the production versions
(`kube.Client`, `kube.CurrentNamespace`) and `os.Stdout/os.Stderr`. See
[`cli.go`](../internal/cli/cli.go).

`commands` (a package-level `[]Command`) is the **single registry** of every
subcommand. `App.Run` (`cli.go`):

1. Intercepts `__complete` (shell completion), `--help`/`--version`, and
   `completion install` — none of these need a cluster.
2. `lookup`s the subcommand (honoring singular/plural aliases via a trailing
   `s` toggle).
3. Registers the global flags, plus `--sort` if the command declares
   `SortColumns`, then parses args.
4. Validates `--sort` against the command's columns and `--color` against
   `auto|always|never`, resolves `Flags.Color` once via `kube.ResolveColor`.
5. Builds the client, applies **namespace defaulting** (below), then calls
   `cmd.Run`.

### The Command registry entry

```go
type Command struct {
    Name             string
    Summary          string
    Run              RunFunc
    CurrentNSDefault bool     // scope to current ns when neither -n nor -A given
    SortColumns      []string // lowercased headers; enables --sort
}
```

Everything registry-driven flows from here: dispatch, the `--help` listing, and
shell completion candidates all read the same slice, so they cannot drift.

### Global flags — one source of truth

Global flags (`--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`,
`--color`) live once in the `globalFlags` table in `cli.go`. That table drives
**both** FlagSet registration and the `--help` listing, so the two can't
diverge. Add a global flag there, not in two places.

### The RunFunc contract

Every subcommand implements one signature (defined in `cli.go`):

```go
type RunFunc func(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error
```

It receives the `kubernetes.Interface` (a real clientset in production, a fake
in tests), the resolved `Flags`, positional args, and the output writer. It
validates its own required positional args (e.g. `on-node` returns a "requires a
node" error).

### Completion (`complete.go`)

`complete.go` implements the cobra-compatible `__complete` protocol kubectl
invokes through the `completion/kubectl_complete-klens` shim. It completes
subcommand names, global flags, `--sort` columns (per the chosen command), and
`--color` values — all derived from the same `commands` registry. `completion
install` writes the shim into krew's bin dir (or `--dir`) and needs no cluster.

## Namespace defaulting

`Command.CurrentNSDefault` controls scoping and is **subtle enough to have a
guard test**:

- `true` + user passed neither `-n` nor `-A` → the dispatcher resolves the
  current kubeconfig namespace (`kube.CurrentNamespace`, i.e. the kubens/kubectx
  namespace) and sets `Flags.Namespace` before running.
- `false` → the command lists across all namespaces by default.

`Flags.NamespaceScope()` (`internal/kube/flags.go`) turns this into the actual
list scope: `-A` → all namespaces (`""`), otherwise the resolved `Namespace`.

The authoritative set of `CurrentNSDefault: true` commands is locked by
`TestCurrentNSDefaultFlags` in `cli_test.go`. **Update that test's map whenever
you change a command's scoping** — it is the source of truth, and CI fails if
the registry and the map disagree.

## Output: the `kube` package

### `Table` (`internal/kube/table.go`)

All columnar output goes through `kube.NewTable(out, painter, headers...)`.
`Row(cols...)` buffers rows; `Flush()` renders them. Two things make it robust:

- **ANSI-aware alignment.** Column widths are measured on *visible* width
  (`stripANSI`), so colored cells still line up.
- **Named-column sort.** `SortBy(column)` sorts ascending by a header name at
  `Flush`, auto-detecting numeric columns so counts order by value. `SortRank`
  registers a custom key for a column whose alphabetical order is meaningless —
  used by verdict commands to order a `VERDICT` column worst-first.

Headers are bolded via the `Painter`. `kube.Label(painter, labels, key)` renders
a label value or a muted `<none>`.

### `Painter` (`internal/kube/color.go`)

`paint := kube.NewPainter(f)` yields a `Painter` whose methods
(`OK`/`Warn`/`Bad`/`Muted`/`Header`) wrap a string in ANSI color — or return it
unchanged when color is disabled or the string is empty. `Painter.Status`
classifies well-known status tokens (`Ready`/`Running` → green, `Pending` →
yellow, `CrashLoopBackOff`/`Unknown`/… → red).

Color is resolved **once** in the dispatcher into `Flags.Color`. Precedence
(`ResolveColor`): explicit `--color` > `KLENS_COLOR` > `NO_COLOR` > TTY
detection. `IsTTY` checks whether the writer is a terminal.

### kubeconfig plumbing (`internal/kube/client.go`)

`clientConfig` builds a deferred-loading `clientcmd.ClientConfig` from the
default loading rules plus the explicit `--kubeconfig` path and `--context`
override. `Client` builds the clientset; `CurrentNamespace` reads the active
context's namespace (defaulting to `default`).

> **client-go auth providers.** `main.go` blank-imports
> `k8s.io/client-go/plugin/pkg/client/auth` to register the non-static auth
> providers (`oidc`, `gcp`, `azure`); exec-based auth is handled by client-go's
> core. Without that import, clusters authenticating via OIDC fail with
> `no Auth Provider found for name "oidc"`.

## The verdict-command pattern

`pdb`, `hpa`, `spread`, `probes`, `pending` share a shape (see
[`internal/view/pdb.go`](../internal/view/pdb.go) as the reference):

1. List the resource, then classify each item with a pure `xVerdict(...)`
   function returning `(verdict, severity)`. The rules are **total** (first match
   wins, a default catch-all), so a verdict is always produced.
2. Severity is one of `ok`/`warn`/`bad`/`muted`, mapped to a `Painter` method by
   `sevPaint`.
3. The `VERDICT` cell is colored by severity; the table gets a `SortRank` on
   `VERDICT` via `verdictRank(worstFirst...)`, and `SortBy(orDefault(f.Sort,
   "verdict"))` defaults to risk order so the riskiest rows sit nearest the
   prompt.

A design principle to preserve: **a control that exists but gives zero
protection must read as bad, not OK** — e.g. a PDB with `DesiredHealthy == 0` on
a multi-replica workload is `NO-GUARD` (red), because a drain can evict every
replica at once. See `pdbVerdict` for the canonical example.

Shared helpers (`orDefault`, `sevPaint`, `verdictRank`) live in
[`internal/view/verdict.go`](../internal/view/verdict.go); `pdb`, `hpa`,
`spread`, and `probes` reuse them (`pending` renders a plain `REASON` column
and only needs `SortBy`). Node helpers (`nodeStatus`, `qtyOrNone`) live in
`view.go`.

## Adding a subcommand

1. Create `internal/view/<name>.go` implementing the `RunFunc` signature; build
   output with `kube.NewTable`/`kube.Label`. Validate required positional args
   inside the func.
2. Register it in the `commands` slice in `internal/cli/cli.go`:
   - set `CurrentNSDefault: true` if it should scope to the current namespace
     (and update `TestCurrentNSDefaultFlags`);
   - set `SortColumns` to the lowercased headers to enable `--sort`, then call
     `t.SortBy(f.Sort)` in the view. `TestSortColumnsMatchHeaders` guards that
     those columns actually exist as headers.
3. Add a `_test.go` next to it. Completion, `--help`, and dispatch are all
   registry-driven — no extra wiring.
4. To color cells, build `paint := kube.NewPainter(f)`, wrap status cells
   (`paint.OK/Warn/Bad/Muted` or `paint.Status`), and pass `paint` to
   `kube.NewTable`. **Name the painter `paint`, not `p`**, to avoid shadowing the
   `p` pod loop variable. Color is off in tests (they pass `kube.Flags{}`), so
   plain-output assertions stay byte-identical — add separate `...Color` tests.
5. Update `README.md`'s usage section (repo convention, before committing).

## Testing

Tests use `k8s.io/client-go/kubernetes/fake.NewClientset(objs...)`, run the
command against a `bytes.Buffer`, and assert on substrings. Dispatcher tests in
`cli_test.go` inject a fake client + an observable `Namespace` resolver and
inspect `clientset.Actions()` to assert the namespace a list was scoped to
(see `listedNamespace` and the `reqlim` tests). Because color is off under
`kube.Flags{}`, plain-output assertions are byte-stable across the color
feature.

## Where to change what

| You want to… | Touch |
|---|---|
| Add/rename a command | `commands` slice in `internal/cli/cli.go` (+ its view file + test) |
| Add a global flag | `globalFlags` table in `cli.go` (drives registration *and* help) |
| Change a command's namespace scope | `CurrentNSDefault` in the registry + `TestCurrentNSDefaultFlags` |
| Change table alignment/sorting | `internal/kube/table.go` |
| Change colors / color precedence | `internal/kube/color.go` |
| Change kubeconfig/context resolution | `internal/kube/client.go` |
| Add/adjust a health verdict | the command's `xVerdict` in `internal/view/<name>.go` |
| Change completion behaviour | `internal/cli/complete.go` |
