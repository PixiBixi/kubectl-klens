# AGENTS.md

Guidance for coding agents working in this repository.

## OpenWiki

[`openwiki/architecture.md`](openwiki/architecture.md) holds the code map;
jump to the section for the task instead of searching the tree:

| Task                                       | Section                                                                                                                                    |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Find which file to touch                   | [Where to change what](openwiki/architecture.md#where-to-change-what)                                                                      |
| Add a command or a `Command` field         | [Adding a subcommand](openwiki/architecture.md#adding-a-subcommand), [registry entry](openwiki/architecture.md#the-command-registry-entry) |
| Change how a table renders, sorts, filters | [`Table`](openwiki/architecture.md#table-internalkubetablego), [verdict pattern](openwiki/architecture.md#the-verdict-command-pattern)     |
| Reuse a pod/node/concurrency helper        | [Shared view helpers](openwiki/architecture.md#shared-view-helpers-internalviewviewgo)                                                     |
| List a resource, push a filter down        | [Listing: paging and pushdown](openwiki/architecture.md#listing-paging-and-pushdown-internalkubelistgo)                                    |
| Write a test                               | [Testing](openwiki/architecture.md#testing)                                                                                                |
| Measure a change                           | [`openwiki/performance.md`](openwiki/performance.md)                                                                                       |

[`openwiki/quickstart.md`](openwiki/quickstart.md) is the user-facing tour and
command catalog.

## What this is

`kubectl-klens` is a single-binary kubectl plugin (`kubectl klens`) bundling 37
read-only cluster-inspection shortcuts. Go 1.27, depends on `client-go` (typed
and dynamic clients), `promptui` (interactive pickers), and `golang.org/x/term`
(TTY detection). No cobra - dispatch is a hand-rolled flag-based switch.

## Common commands

```bash
make build      # go build -ldflags "-s" -o kubectl-klens .
make test       # go test -race ./...
make bench      # go test -bench (BENCH=<re> COUNT=<n>); see openwiki/performance.md
make lint       # golangci-lint run (config: .golangci.yml)
make lint-md FILES="README.md AGENTS.md"  # markdownlint, as the CI job runs it
make snapshot   # goreleaser release --snapshot --clean (dry-run)

go test -race ./internal/view -run TestNodes   # single test
```

`Taskfile.yml` mirrors the Makefile (`task build`, `task test`, ...). `make lint`
runs **golangci-lint** (`.golangci.yml`, `version: "2"`) - the same linter the
`Lint` CI job enforces. CI is split across `.github/workflows/`: `ci.yml`
(`go mod verify`, build, `go test -race`), `lint.yml` (golangci-lint), plus
`go-format`, `govulncheck`, `github-actions`, and `markdownlint` workflows.

## Architecture

Three packages under `internal/`, layered cli → view → kube:

- **`internal/cli`** - the dispatcher. `App` holds injected `NewClient` and
  `Namespace` functions so `Run` is testable without a real cluster (see
  `NewApp` for the production wiring). `commands` (a package-level slice) is the
  single registry of `Command` entries; `Run` parses global flags (on either
  side of positionals, `parseInterspersed`), builds the client, applies
  namespace defaulting, then calls the command's `RunFunc`. A command opts into
  behavior through its `Command` fields, each locked by a test in `cli_test.go`
  that is the authoritative list of commands setting it:

  | Field              | Effect                                                                        | Guard test                    | The view must                                 |
  | ------------------ | ----------------------------------------------------------------------------- | ----------------------------- | --------------------------------------------- |
  | `CurrentNSDefault` | scope to the current kubeconfig namespace when neither `-n` nor `-A` is given | `TestCurrentNSDefaultFlags`   | nothing                                       |
  | `SortColumns`      | `--sort <column>`, validated against the list, passed as `kube.Flags.Sort`    | `TestSortColumnsMatchHeaders` | call `t.SortBy(f.Sort)` before `Flush`        |
  | `NameColumns`      | positionals are names/globs, passed as `kube.Flags.Names`                     | `TestNameColumnsMatchHeaders` | call `t.FilterBy(f.NameColumns, f.Names)`     |
  | `OwnArgs`          | positionals reach the view untouched (any other command rejects them)         | -                             | validate its args                             |
  | `Watch`            | `-w/--watch` + `--interval`, refused on a non-TTY stdout, loop in `watch.go`  | `TestWatchFlags`              | nothing (re-run into a buffer every interval) |
  | `ByOwner`          | `--by-owner`: rows come from controllers, not pods (`byowner.go`)             | `TestByOwnerFlags`            | read nothing but the pod spec                 |
  | `IgnoresNamespace` | cluster-scoped objects only, `-n` resolution skipped                          | `TestIgnoresNamespaceFlags`   | nothing                                       |

  Views flush through more than one path (`t.Flush`, `flushVerdicts`,
  `flushNetpol`, `renderAutoscalerStatus`, `renderNodes`/`nodeTable`): a
  per-table behavior goes in every one of them.
  Global flags (`-n`, `--context`, ...) live once in the `globalFlags` table,
  which drives both FlagSet registration and the `--help` listing so the two
  can't drift - add a global flag there, not in two places. `complete.go`
  implements the cobra-compatible `__complete` protocol kubectl invokes via the
  `completion/kubectl_complete-klens` shim, plus `completion install` (writes
  the shim to krew's bin dir, needs no cluster). Completion after `-n` is the
  one candidate source that hits the cluster: 2s timeout, and silent (no
  candidates, no message) on any failure - its output lands on the shell's
  command line.
- **`internal/view`** - one file per subcommand, each a `RunFunc`:
  `func(ctx, kube.Clients, kube.Flags, args []string, out io.Writer) error`.
  Shared node helpers live in `view.go`. `byowner.go` holds `podsForView`, the
  shared source for the `--by-owner` commands: it lists Deployments, StatefulSets,
  DaemonSets, Argo Rollouts, Strimzi PodSets and CloudNativePG Clusters instead
  of pods and turns each into a synthetic pod (Namespace/Name from the
  controller, Spec its template, Status zero), which then flows through the
  view's normal per-container loop unmodified - a view qualifies only if it
  reads nothing but the container spec, `qos` included (its `qosClass` has a
  from-spec fallback for exactly this). A CNPG Cluster has no pod template at
  all - its synthetic pod is reconstructed from `spec.resources` alone, and the
  row is marked unknown for `probes`/`images` instead of guessing them.
  `secret.go` is the only interactive command: `kube.IsTTY(out)` gates promptui
  pickers vs. plain piped listings.
  Sortable views call `t.SortBy(f.Sort)` before `Flush`; `image-count` and
  `restarts` keep a bespoke count-descending default (overridden by `--sort`).
  Views colorize status cells by building `paint := kube.NewPainter(f)` and
  wrapping cells (`paint.OK/Warn/Bad/Muted` or the `paint.Status` classifier).
- **`internal/kube`** - kubeconfig plumbing (`NewClients`, `CurrentNamespace`,
  `clientConfig` via deferred loading rules + context override), the `Clients`
  bundle (embedded `kubernetes.Interface` + `Dynamic` for CRDs), the `Flags`
  struct with `Scope()`, `scope.go` (`ResolveScope` expands and validates `-n`,
  including globs) plus the `listScoped` fan-out that turns a multi-namespace
  `Scope` into one `List` per namespace up to `MaxNamespaceFanout` (with
  `MaxInFlight` capping total concurrent requests, since `cfg.QPS = -1` means
  nothing else does and the fan-out layers multiply), the
  `Table` helper used for all columnar output, and `color.go` (`Painter` +
  `ResolveColor` + `IsTTY`). `Table` buffers rows and, via `SortBy(column)`,
  sorts ascending by a named header at `Flush` (numeric columns ordered by
  value); it aligns on *visible* width (ANSI stripped) so colored cells don't
  break columns, and bolds headers via the
  `Painter` passed to `NewTable`. Color is resolved once in the dispatcher
  (`--color` > `KLENS_COLOR` > `NO_COLOR` > TTY) into `Flags.Color`.

### Namespace defaulting (subtle, has a guard test)

`Command.CurrentNSDefault` controls scoping. When `true` and the user passed
neither `-n` nor `-A`, the dispatcher resolves the current kubeconfig namespace
(kubens/kubectx) before running. When `false`, the command lists all namespaces
by default. `TestCurrentNSDefaultFlags` in `cli_test.go` holds the
authoritative set - update that map whenever you change a command's scoping.

Separately, `kube.ResolveScope` validates `-n` against the cluster before every
command that does not set `IgnoresNamespace`: an unknown namespace, or a glob
matching none, is an error rather than an empty table. Dispatcher tests must
therefore seed `Namespace` objects into the fake (`namespaceObjs` in
`cli_test.go`).

## Adding a subcommand

1. Create `internal/view/<name>.go` implementing the `RunFunc` signature; use
   `kube.NewTable`/`kube.Label` for output. Validate required positional args
   inside the func (see `OnNode` returning a "requires a node" error).
2. Register it in the `commands` slice in `internal/cli/cli.go` (set `CurrentNSDefault`
   if it should scope to the current namespace; set `SortColumns` to the
   lowercased headers to enable `--sort`, then call `t.SortBy(f.Sort)` in the
   view; set `Watch: true` only if the answer changes while you watch it, and
   update `TestWatchFlags`; set `ByOwner: true` only for a view whose rows are
   pod *spec* - a view of runtime state would hide the one pod that differs -
   and update `TestByOwnerFlags`). `TestSortColumnsMatchHeaders` guards that
   those columns exist, in both `--by-owner` modes. Set `NameColumns` (and call
   `t.FilterBy(f.NameColumns, f.Names)` before `Flush`) or `OwnArgs`, otherwise
   a positional arg is an error.
3. Add a `_test.go` next to it. Shell completion, `--help`, and dispatch are all
   registry-driven - no extra wiring.
4. Anything called once per pod takes the field it reads, not `kube.Flags` by
   value: the struct is ~120 bytes and the copy measured +3.6% on
   `BenchmarkReqlim` (see `openwiki/performance.md`).
5. To color cells, build `paint := kube.NewPainter(f)`, wrap status cells
   (`paint.OK/Warn/Bad/Muted` or the `paint.Status` classifier), and pass `paint`
   to `kube.NewTable`. Name the painter `paint`, not `p`, to avoid shadowing the
   `p` pod loop variable. Color is off in tests (they pass `kube.Flags{}`), so
   plain-output assertions stay byte-identical - add new `...Color` tests instead.
6. Update the docs, before committing: the README usage section (repo
   convention), the `openwiki/quickstart.md` command catalog, and any
   `openwiki/architecture.md` section the change reaches - its pushdown table
   when the view uses a field selector, its Testing section when you touch the
   shared fake helpers. A doc naming a helper that no longer exists is worse than
   no doc.

## Testing pattern

Tests use `k8s.io/client-go/kubernetes/fake.NewClientset(objs...)`, run the
command writing to a `bytes.Buffer`, and assert on substrings. Dispatcher tests
inject a fake client + observable `Namespace` resolver and inspect
`clientset.Actions()` to assert the namespace a list was scoped to. Helpers:

- `internal/cli/cli_test.go`: `testApp(out, errw)` builds an `App` on a fake,
  `namespaceObjs` seeds the namespaces `ResolveScope` checks, and
  `listedNamespace(s)` reads back the scope of the lists issued.
- `internal/view/fake_test.go`: `clients(c)` wraps a clientset into
  `kube.Clients`; `newClientsetWithFieldSelectors` + `assertFieldSelector`
  test a pushdown view, because the plain fake ignores field selectors.

## Releasing

Releases are **automatic** on push to `master`. `.github/workflows/release.yml`
runs [`svu`](https://github.com/caarlos0/svu) to compute the next `vX.Y.Z` from
the conventional commits since the last tag (`feat` → minor, `fix` → patch;
anything else produces nothing, which is the gate on the following steps),
creates the tag through the API, then runs goreleaser in the same job - no
separate PAT needed because a `GITHUB_TOKEN`-created tag would not re-trigger a
workflow. Pushing a `v*` tag by hand still works as a manual escape hatch (the
job's goreleaser steps also fire on `ref_type == 'tag'`).

Note that **`perf:` does not cut a release**: svu implements the Conventional
Commits spec, where only `fix` and `feat` are normative. Use `fix:` for a change
that has to ship on its own. `--v0` also stops a breaking change from jumping
straight to `v1.0.0` while the project is pre-1.0.

goreleaser builds cross-platform archives and pushes the regenerated
`plugins/klens.yaml` to the central
[PixiBixi/krew-index](https://github.com/PixiBixi/krew-index) repo (via the `krews`
publisher, using the `KREW_INDEX_TOKEN` PAT secret for the cross-repo push). That
is how users `kubectl krew upgrade klens`. Version/commit/date are
injected via `-X main.version=...` ldflags.

Renovate drives the version bumps: `renovate.json` maps minor Go-module updates to
`feat(deps):` (minor release) and patch/digest to `fix(deps):` (patch release);
GitHub Actions updates stay `chore(deps):` (automerged, **no** release - they don't
ship in the binary). All minor/patch/digest updates automerge via PR once CI passes.
