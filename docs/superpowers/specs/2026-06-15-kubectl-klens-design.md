# kubectl-klens — Design Spec

Date: 2026-06-15
Status: Approved

## Goal

A kubectl plugin, written in Go, bundling frequently-used read-only cluster
inspection commands behind a single dispatcher. Distributed as a krew plugin.

Invocation: `kubectl klens <subcommand>`.

## Distribution

- **Krew-ready from day one**: ships a `.krew.yaml` manifest, GitHub release
  archives (`tar.gz`), and `checksums.txt`.
- **Phase 1 — personal install**: `kubectl krew install --manifest=.krew.yaml --archive=<tarball>`.
- **Phase 2 — upstream (later)**: PR to `kubernetes-sigs/krew-index` via
  `krew-release-bot`. Wired in the release workflow but only triggers when the
  user decides to point it upstream.

## Stack (mirrors `github.com/PixiBixi/kubearch` conventions)

- Go 1.26, module `github.com/PixiBixi/kubectl-klens`.
- Cluster access via **client-go** (`k8s.io/client-go` v0.35.x, `k8s.io/api`,
  `k8s.io/apimachinery`). No shell-out to `kubectl` — self-contained binary.
- CLI: stdlib `flag` + manual subcommand dispatch (no cobra). `slog` for errors.
- Kubeconfig/context/namespace via `clientcmd.NewNonInteractiveDeferredLoadingClientConfig`
  + `ConfigOverrides` (same pattern as kubearch `buildK8sClient`). Exposes the
  standard flags: `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`.
- Table output via stdlib `text/tabwriter` (replaces the bash `column -t`).

## Layout

```text
kubectl-klens/
├── main.go                    # version/commit/date vars (ldflags), flag parse, dispatch
├── internal/
│   ├── cli/                   # subcommand registry, usage/help, arg parsing
│   ├── kube/                  # client builder + shared helpers (label lookup, qty fmt)
│   └── view/                  # one file per subcommand (+ _test.go)
├── .goreleaser.yml
├── .github/workflows/
│   ├── ci.yml                 # go mod verify, build, vet, staticcheck, test -race
│   └── release.yml            # goreleaser on tag v* (+ krew-release-bot step)
├── .krew.yaml                 # krew manifest template
├── Makefile
├── .pre-commit-config.yaml
├── .gitignore
├── README.md
├── LICENSE
└── CHANGELOG.md
```

## Subcommands (9, read-only)

All re-implemented against the API via client-go. Output is a tabwriter table.

| Subcommand        | Resource(s)                | Columns / behavior                                                                 |
|-------------------|----------------------------|------------------------------------------------------------------------------------|
| `nodes`           | nodes                      | NAME, STATUS, NODEPOOL (`cloud.google.com/gke-nodepool`), INSTANCE-TYPE (`node.kubernetes.io/instance-type`) |
| `taints`          | nodes                      | NAME, TAINTS (`key=value:effect`, comma-joined)                                    |
| `capacity`        | nodes                      | NAME, CPU_CAP, CPU_ALLOC, MEM_CAP, MEM_ALLOC                                        |
| `zones`           | nodes                      | NAME, REGION (`topology.kubernetes.io/region`), ZONE (`topology.kubernetes.io/zone`) |
| `pods-per-node`   | pods (all ns)              | NODE, PODS (count), sorted desc                                                     |
| `reqlim`          | pods (current ns)          | NS, POD, CONTAINER, REQ_CPU, LIM_CPU, REQ_MEM, LIM_MEM — excludes `kube-system`; current namespace by default, `-A` for all |
| `images`          | pods (all ns)              | COUNT, IMAGE — occurrences across the cluster, sorted desc                          |
| `on-node <node>`  | pods (field selector)      | NS, POD, STATUS, NODE — pods scheduled on `<node>`; node arg required               |
| `pvc`             | pods + pvc (all ns)        | NS, POD, NODE, PVC — PVCs bound to a pod and its node                               |

## Global flags & behavior

- `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`, `--help`, `--version`.
- No subcommand or unknown subcommand → usage to stderr + exit 1.
- `on-node` without a node argument → explicit error + exit 1.
- Node-scoped commands (`nodes`, `taints`, `capacity`, `zones`) ignore namespace.
- Default scope for most pod-scoped commands (`pods-per-node`, `images`,
  `on-node`, `pvc`): all namespaces (matches the original wiki one-liners which
  used `-A`); `-n` narrows it.
- `reqlim` is the exception: it defaults to the current kubeconfig namespace
  (the one set by kubens/kubectx, resolved via `clientcmd.Namespace()`); `-A`
  widens it to all namespaces and `-n` targets a specific one.

## Testing

- Go tests using `k8s.io/client-go/kubernetes/fake`. Seed fake nodes/pods/pvcs,
  assert tabwriter output and dispatch logic. No live cluster required.
- Dispatch tests: unknown subcommand → exit 1; `--help` lists all subcommands;
  `on-node` without arg errors.
- Lint: `staticcheck` + `go vet` (kubearch convention; no golangci-lint).

## CI/CD

- **ci.yml** (push/PR to main/master): `setup-go 1.26`, `go mod verify`,
  `go build ./...`, `go vet ./...`, install + run `staticcheck`, `go test -race ./...`.
- **release.yml** (tag `v*`): `goreleaser-action@v6` (`~> v2`) → builds
  linux/darwin × amd64/arm64 (`CGO_ENABLED=0`, ldflags inject version/commit/date),
  `tar.gz` archives (with `kubectl-klens` binary + README + LICENSE), `checksums.txt`,
  conventional-commit changelog groups (feat/fix/others). Then a `krew-release-bot`
  step (present, fires only when upstreaming).
- **.krew.yaml**: manifest `name: klens`, `shortDescription`, `homepage`,
  per-platform (darwin/linux × amd64/arm64) selecting the matching archive +
  `bin: kubectl-klens`.

## Versioning

- ldflags `-X main.version -X main.commit -X main.date` (kubearch pattern).
- Conventional commits; goreleaser changelog grouping.

## Out of scope (YAGNI for v1)

- No cobra, no prometheus/metrics, no ko/container image, no helm chart
  (those belong to a daemon like kubearch, not a short-lived CLI plugin).
- No arbitrary kubectl flag pass-through beyond the standard ones above.
- Destructive wiki commands (delete pods, force-refresh ExternalSecrets) are
  intentionally excluded — this plugin is read-only.

## Implementation phases & model selection

| Phase | Content | Model |
|-------|---------|-------|
| 0 | Repo scaffold: go.mod, main.go, `internal/kube` + `internal/cli` | Opus (architecture) |
| 1 | The 9 subcommands + fake-clientset tests (pattern work on kubearch) | Sonnet |
| 2 | goreleaser + workflows + .krew.yaml + Makefile + README | Sonnet |
| 3 | Integration: wiring, `go build`, `go test`, staticcheck, final review | Opus |
