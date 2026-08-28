# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## OpenWiki

This repository has documentation located in the /openwiki directory.

Start here:
- [OpenWiki quickstart](openwiki/quickstart.md)

OpenWiki includes repository overview, architecture notes, workflows, domain concepts, operations, integrations, testing guidance, and source maps.

When working in this repository, read the OpenWiki quickstart first, then follow its links to the relevant architecture, workflow, domain, operation, and testing notes.

## What this is

`kubectl-klens` is a single-binary kubectl plugin (`kubectl klens`) bundling ~34
read-only cluster-inspection shortcuts. Go 1.27, depends on `client-go` (typed
and dynamic clients), `promptui` (interactive pickers), and `golang.org/x/term`
(TTY detection). No cobra - dispatch is a hand-rolled flag-based switch.

## Common commands

```bash
make build      # go build -ldflags "-s" -o kubectl-klens .
make test       # go test -race ./...
make bench      # go test -bench (BENCH=<re> COUNT=<n>); see openwiki/performance.md
make lint       # golangci-lint run (config: .golangci.yml)
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
  single registry of `Command` entries; `Run` parses global flags, builds the
  client, applies namespace defaulting, then calls the command's `RunFunc`. A
  command that sets `SortColumns` opts into `--sort <column>`: the dispatcher
  registers the flag, validates the value against that list, and the value flows
  through `kube.Flags.Sort`. A command that sets `Watch` opts into `-w/--watch` +
  `--interval`: the dispatcher refuses both on a non-TTY stdout and hands
  `cmd.Run` to the redraw loop in `watch.go`, which re-polls into a buffer every
  interval; `TestWatchFlags` locks the watchable set (`pending`, `restarts`,
  `rollouts`, `terminating`, `autoscaler`, `node-conditions`, `svc-backends`,
  `max-pods`, `pvc-resize`). Global flags (`-n`, `--context`, ...) live once in
  the `globalFlags` table, which drives both FlagSet registration and the
  `--help` listing so the two can't drift - add a global flag there, not in two
  places. `complete.go`
  implements the cobra-compatible `__complete` protocol kubectl invokes via the
  `completion/kubectl_complete-klens` shim, plus `completion install` (writes
  the shim to krew's bin dir, needs no cluster).
- **`internal/view`** - one file per subcommand, each a `RunFunc`:
  `func(ctx, kube.Clients, kube.Flags, args []string, out io.Writer) error`.
  Shared node helpers live in `view.go`. `secret.go` is the only interactive
  command: `kube.IsTTY(out)` gates promptui pickers vs. plain piped listings.
  Sortable views call `t.SortBy(f.Sort)` before `Flush`; `image-count` and
  `restarts` keep a bespoke count-descending default (overridden by `--sort`).
  Views colorize status cells by building `paint := kube.NewPainter(f)` and
  wrapping cells (`paint.OK/Warn/Bad/Muted` or the `paint.Status` classifier).
- **`internal/kube`** - kubeconfig plumbing (`NewClients`, `CurrentNamespace`,
  `clientConfig` via deferred loading rules + context override), the `Clients`
  bundle (embedded `kubernetes.Interface` + `Dynamic` for CRDs), the `Flags`
  struct with `NamespaceScope()`, the `Table` helper used for all columnar
  output, and `color.go` (`Painter` + `ResolveColor` + `IsTTY`). `Table` buffers
  rows and, via `SortBy(column)`, sorts ascending by a named header at `Flush`
  (numeric columns ordered by value); it aligns on *visible* width (ANSI
  stripped) so colored cells don't break columns, and bolds headers via the
  `Painter` passed to `NewTable`. Color is resolved once in the dispatcher
  (`--color` > `KLENS_COLOR` > `NO_COLOR` > TTY) into `Flags.Color`.

### Namespace defaulting (subtle, has a guard test)

`Command.CurrentNSDefault` controls scoping. When `true` and the user passed
neither `-n` nor `-A`, the dispatcher resolves the current kubeconfig namespace
(kubens/kubectx) before running. When `false`, the command lists all namespaces
by default. The current `CurrentNSDefault` set (`reqlim`, `no-limits`,
`no-requests`, `images`, `restarts`, `pvc`, `pvc-unused`, `pvc-resize`,
`svc-fqdn`, `svc-backends`, `ingress`, `secret`, `privileged`, `pdb`, `pending`,
`hpa`, `spread`, `probes`, `qos`, `rollouts`, `unused-config`) is locked in by
`TestCurrentNSDefaultFlags` in `cli_test.go`, which is the authoritative list -
update that map whenever you change a command's scoping.

## Adding a subcommand

1. Create `internal/view/<name>.go` implementing the `RunFunc` signature; use
   `kube.NewTable`/`kube.Label` for output. Validate required positional args
   inside the func (see `OnNode` returning a "requires a node" error).
2. Register it in the `commands` slice in `internal/cli/cli.go` (set `CurrentNSDefault`
   if it should scope to the current namespace; set `SortColumns` to the
   lowercased headers to enable `--sort`, then call `t.SortBy(f.Sort)` in the
   view; set `Watch: true` only if the answer changes while you watch it, and
   update `TestWatchFlags`). `TestSortColumnsMatchHeaders` guards that those
   columns exist.
3. Add a `_test.go` next to it. Shell completion, `--help`, and dispatch are all
   registry-driven - no extra wiring.
4. To color cells, build `paint := kube.NewPainter(f)`, wrap status cells
   (`paint.OK/Warn/Bad/Muted` or the `paint.Status` classifier), and pass `paint`
   to `kube.NewTable`. Name the painter `paint`, not `p`, to avoid shadowing the
   `p` pod loop variable. Color is off in tests (they pass `kube.Flags{}`), so
   plain-output assertions stay byte-identical - add new `...Color` tests instead.
5. Update the docs, before committing: the README usage section (repo
   convention), the `openwiki/quickstart.md` command catalog, and any
   `openwiki/architecture.md` section the change reaches - its pushdown table
   when the view uses a field selector, its Testing section when you touch the
   shared fake helpers. A doc naming a helper that no longer exists is worse than
   no doc.

## Testing pattern

Tests use `k8s.io/client-go/kubernetes/fake.NewClientset(objs...)`, run the
command writing to a `bytes.Buffer`, and assert on substrings. Dispatcher tests
inject a fake client + observable `Namespace` resolver and inspect
`clientset.Actions()` to assert the namespace a list was scoped to (see
`listedNamespace` and the `reqlim` tests in `cli_test.go`).

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
is how users `kubectl krew upgrade pixibixi/klens`. Version/commit/date are
injected via `-X main.version=...` ldflags.

Renovate drives the version bumps: `renovate.json` maps minor Go-module updates to
`feat(deps):` (minor release) and patch/digest to `fix(deps):` (patch release);
GitHub Actions updates stay `chore(deps):` (automerged, **no** release - they don't
ship in the binary). All minor/patch/digest updates automerge via PR once CI passes.
