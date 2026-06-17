# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`kubectl-klens` is a single-binary kubectl plugin (`kubectl klens`) bundling ~14
read-only cluster-inspection shortcuts. Go 1.26, depends on `client-go`,
`promptui` (interactive pickers), and `golang.org/x/term` (TTY detection). No
cobra — dispatch is a hand-rolled flag-based switch.

## Common commands

```bash
make build      # go build -ldflags "-s -w" -o kubectl-klens .
make test       # go test -race ./...
make lint       # go vet ./... && staticcheck ./...
make snapshot   # goreleaser release --snapshot --clean (dry-run)

go test -race ./internal/view -run TestNodes   # single test
```

`Taskfile.yml` mirrors the Makefile (`task build`, `task test`, ...). CI runs
`go mod verify`, build, `go vet`, `staticcheck`, and `go test -race`; staticcheck
must pass.

## Architecture

Three packages under `internal/`, layered cli → view → kube:

- **`internal/cli`** — the dispatcher. `App` holds injected `NewClient` and
  `Namespace` functions so `Run` is testable without a real cluster (see
  `NewApp` for the production wiring). `commands()` is the single registry of
  `Command` entries; `Run` parses global flags, builds the client, applies
  namespace defaulting, then calls the command's `RunFunc`. A command that sets
  `SortColumns` opts into `--sort <column>`: the dispatcher registers the flag,
  validates the value against that list, and the value flows through
  `kube.Flags.Sort`. `complete.go`
  implements the cobra-compatible `__complete` protocol kubectl invokes via the
  `completion/kubectl_complete-klens` shim, plus `completion install` (writes
  the shim to krew's bin dir, needs no cluster).
- **`internal/view`** — one file per subcommand, each a `RunFunc`:
  `func(ctx, kubernetes.Interface, kube.Flags, args []string, out io.Writer) error`.
  Shared node helpers live in `view.go`. `secret.go` is the only interactive
  command: `isTTY(out)` gates promptui pickers vs. plain piped listings.
  Sortable views call `t.SortBy(f.Sort)` before `Flush`; `image-count` is the
  exception, doing a bespoke count-descending sort in `imageCountLess`.
- **`internal/kube`** — kubeconfig plumbing (`Client`, `CurrentNamespace`,
  `clientConfig` via deferred loading rules + context override), the `Flags`
  struct with `NamespaceScope()`, and the `Table` helper used for all columnar
  output. `Table` buffers rows and, via `SortBy(column)`, sorts ascending by a
  named header at `Flush` (numeric columns ordered by value).

### Namespace defaulting (subtle, has a guard test)

`Command.CurrentNSDefault` controls scoping. When `true` and the user passed
neither `-n` nor `-A`, the dispatcher resolves the current kubeconfig namespace
(kubens/kubectx) before running. When `false`, the command lists all namespaces
by default. Today `reqlim`, `svc-fqdn`, `secret`, `pvc`, `images` are
`CurrentNSDefault`; `TestCurrentNSDefaultFlags` in `cli_test.go` locks this set
in — update it when you change a command's scoping.

## Adding a subcommand

1. Create `internal/view/<name>.go` implementing the `RunFunc` signature; use
   `kube.NewTable`/`kube.Label` for output. Validate required positional args
   inside the func (see `OnNode` returning a "requires a node" error).
2. Register it in `commands()` in `internal/cli/cli.go` (set `CurrentNSDefault`
   if it should scope to the current namespace; set `SortColumns` to the
   lowercased headers to enable `--sort`, then call `t.SortBy(f.Sort)` in the
   view). `TestSortColumnsMatchHeaders` guards that those columns exist.
3. Add a `_test.go` next to it. Shell completion, `--help`, and dispatch are all
   registry-driven — no extra wiring.
4. Update the README usage section (per repo convention, before committing).

## Testing pattern

Tests use `k8s.io/client-go/kubernetes/fake.NewClientset(objs...)`, run the
command writing to a `bytes.Buffer`, and assert on substrings. Dispatcher tests
inject a fake client + observable `Namespace` resolver and inspect
`clientset.Actions()` to assert the namespace a list was scoped to (see
`listedNamespace` and the `reqlim` tests in `cli_test.go`).

## Releasing

Push a `v*` tag → `.github/workflows/release.yml` runs goreleaser, which builds
cross-platform archives and regenerates `plugins/klens.yaml` (committed back to
`master`). The repo doubles as a krew custom index, so that committed manifest is
how users `kubectl krew upgrade pixibixi/klens`. Upstreaming to the central
krew-index is wired but disabled (`if: false`). Version/commit/date are injected
via `-X main.version=...` ldflags.
