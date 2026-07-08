# kubectl-klens — Quickstart

`kubectl-klens` is a single-binary **kubectl plugin** (`kubectl klens`) bundling
~24 read-only cluster-inspection shortcuts behind one dispatcher. It is the
codified form of a pile of "quick look at the cluster" one-liners: nodes,
capacity, requests/limits, images, restarts, PVCs, and a set of *verdict*
commands (`pdb`, `hpa`, `spread`, `probes`, `pending`) that classify a
resource's health at a glance instead of making you read raw status fields.

- **Language / runtime:** Go 1.26, compiled to a static `kubectl-klens` binary.
- **Key deps:** `k8s.io/client-go` (cluster access), `manifoldco/promptui`
  (interactive pickers, `secret` only), `golang.org/x/term` (TTY detection).
- **No cobra:** dispatch is a hand-rolled flag-based `switch` over a package-level
  command registry.
- **Read-only:** every command only *lists/reads* cluster state; nothing mutates
  the cluster.

Entry point: `main.go` injects ldflags version metadata into `cli.NewApp(...)`
and calls `App.Run(os.Args[1:])`.

## Install

Published to the [PixiBixi krew custom index](https://github.com/PixiBixi/krew-index):

```bash
kubectl krew index add pixibixi https://github.com/PixiBixi/krew-index.git
kubectl krew install pixibixi/klens
kubectl krew upgrade klens          # later, to update
```

Or drop the `kubectl-klens` binary from a release archive onto your `PATH` and
call it as `kubectl klens`.

## Command catalog

The authoritative list is the `commands` slice in
[`internal/cli/cli.go`](../internal/cli/cli.go). Grouped by what they inspect:

**Nodes / capacity**
- `nodes` — nodes + GKE nodepool + instance-type
- `taints` — taints per node
- `capacity` — CPU/mem capacity + allocatable per node
- `zones` — region/zone per node
- `pods-per-node` — pod count per node
- `max-pods` — pod ceiling (allocatable), current count, free slots per node
- `node-conditions` — readiness + memory/disk/pid pressure
- `on-node <node>` — pods scheduled on a given node
- `autoscaler` — cluster-autoscaler status (always reads `kube-system`)

**Workload hygiene (namespace-scoped)**
- `reqlim` — requests/limits per container
- `no-limits` / `no-requests` — containers missing limits / requests
- `images` — image per container per pod
- `image-count` — image occurrence counts split registry/image/tag (cluster-wide)
- `restarts` — restarted containers + crash reason
- `pvc` — PVCs bound to pod + node
- `default-sa` — pods still on the default service account
- `privileged` — containers with privileged/host security flags
- `svc-fqdn` — in-cluster FQDN of services

**Verdict commands** (compute a health classification, default-sorted worst-last)
- `pdb` — PodDisruptionBudget drain-safety verdict
- `pending` — Pending pods with a synthesized blocking reason
- `hpa` — HorizontalPodAutoscaler autoscaling verdict
- `spread` — replica placement single-point-of-failure verdict
- `probes` — readiness/liveness/startup probe reliability verdict

**Interactive**
- `secret` — browse secrets interactively (pick secret, then key); positional
  args skip the pickers. The only command that draws promptui pickers, and only
  when stdout is a TTY.

`kubectl klens --help` prints the same catalog (it is generated from the
registry, so it can't drift). Subcommands accept singular/plural aliases
(`image` ≡ `images`).

## Cross-cutting behaviour

These four behaviours apply across commands — learn them once.

### Namespace defaulting
Some commands default to the **current kubeconfig namespace** (the one set by
kubens/kubectx); the rest default to **all namespaces**.

- Current-namespace-by-default: `reqlim`, `no-limits`, `no-requests`, `images`,
  `restarts`, `pvc`, `svc-fqdn`, `secret`, `privileged`, `pdb`, `pending`, `hpa`,
  `spread`, `probes`.
- `-A` / `--all-namespaces` widens to all; `-n <ns>` targets one.
- `autoscaler` ignores namespace flags entirely (always `kube-system`).

This is driven by `Command.CurrentNSDefault` and is locked by a guard test —
see [architecture.md](architecture.md#namespace-defaulting).

### Sorting (`--sort`)
A command that declares `SortColumns` opts into `--sort <column>`. The
dispatcher registers the flag, validates the value against that command's
columns, and the view sorts by it. Verdict commands default to sorting by their
`VERDICT` column in risk order (riskiest rows land at the bottom, nearest the
prompt).

```bash
kubectl klens image-count --sort registry
kubectl klens pdb --sort verdict
```

### Color
Tables colorize status cells: green = good, yellow = warning, red = bad, gray =
muted placeholders, bold = headers. Control with
`--color=auto|always|never` (default `auto` = color only when stdout is a TTY).
`NO_COLOR` disables; `KLENS_COLOR` sets the default via the environment.

Under kubecolor, klens' stdout is a pipe so `auto` turns color off; force it
with `--color=always` or `export KLENS_COLOR=always` (kubecolor passes plugin
output through unchanged).

### Shell completion
`kubectl klens <TAB>` uses kubectl's plugin-completion mechanism (kubectl 1.26+):
kubectl runs an executable `kubectl_complete-klens` on your `PATH`, which
forwards to the plugin's hidden `__complete` command. Install the shim (no
cluster needed):

```bash
kubectl klens completion install                 # into krew's bin dir
kubectl klens completion install --dir /usr/local/bin
```

Load kubectl's own completion first, e.g. `source <(kubectl completion zsh)`.

## Develop / test

```bash
make build      # go build -ldflags "-s -w" -o kubectl-klens .
make test       # go test -race ./...
make lint       # go vet ./... && staticcheck ./...
make snapshot   # goreleaser --snapshot --clean (dry-run)

go test -race ./internal/view -run TestNodes    # single test
```

`Taskfile.yml` mirrors the Makefile (`task build`, `task test`, ...). CI runs
`go mod verify`, build, `go vet`, `staticcheck`, and `go test -race`;
**staticcheck must pass**.

## Release

Push a `v*` tag → `.github/workflows/release.yml` runs goreleaser, which builds
cross-platform archives and pushes the regenerated `plugins/klens.yaml` to the
central [PixiBixi/krew-index](https://github.com/PixiBixi/krew-index) repo (the
`krews` publisher, using the `KREW_INDEX_TOKEN` PAT for the cross-repo push).
Version/commit/date are injected via `-X main.version=...` ldflags. Convention:
push a fresh `vX.Y.Z` tag after every functional change to `master`.

## Where to go next

- **[architecture.md](architecture.md)** — the cli→view→kube layering, the
  `RunFunc` contract, the `Table`/`Painter` output mechanics, the
  verdict-command pattern, and a step-by-step guide to adding a subcommand.
