# kubectl-klens - Quickstart

`kubectl-klens` is a single-binary **kubectl plugin** (`kubectl klens`) bundling
~25 read-only cluster-inspection shortcuts behind one dispatcher. It is the
codified form of a pile of "quick look at the cluster" one-liners: nodes,
capacity, requests/limits, images, restarts, PVCs, and a set of *verdict*
commands (`pdb`, `hpa`, `spread`, `probes`, `qos`, `svc-backends`, `rollouts`,
`ingress`, `terminating`, `pending`) that classify a resource's health at a
glance instead of making you read raw status fields.

- **Language / runtime:** Go 1.27, compiled to a static `kubectl-klens` binary.
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
- `nodes` - nodes + GKE nodepool + instance-type
- `taints` - taints per node
- `capacity` - CPU/mem capacity + allocatable per node
- `zones` - region/zone per node
- `node-ips [node]` - internal + external IP per node, or for a single node
  (`<none>` when a node has no public address)
- `pods-per-node` - pod count per node
- `max-pods` - pod ceiling (allocatable), current count, free slots per node
- `node-conditions` - readiness + memory/disk/pid pressure
- `on-node <node>` - pods scheduled on a given node
- `autoscaler` - cluster-autoscaler status (always reads `kube-system`)

**Workload hygiene (namespace-scoped)**
- `reqlim` - requests/limits per container
- `no-limits` / `no-requests` - containers missing limits / requests
- `images` - image per container per pod
- `image-count` - image occurrence counts split registry/image/tag (cluster-wide)
- `unused-config` - ConfigMaps/Secrets nothing references (pod templates
  included, so a CronJob between runs is not a false positive), biggest first
- `restarts` - restarted containers + crash reason + last exit code (137/143 = SIGKILL/SIGTERM)
- `qos` - QoS class + pod requests/limits totals + eviction-risk verdict
- `pvc` - PVCs bound to pod + node
- `pvc-unused` - PVCs no pod mounts (`ORPHAN` is reclaimable, `SCALED-DOWN` a
  StatefulSet leftover), with capacity and storage class
- `default-sa` - pods still on the default service account
- `privileged` - containers with privileged/host security flags
- `svc-fqdn` - in-cluster FQDN of services

`reqlim`, `no-limits`, `no-requests`, `images`, `restarts` and `privileged`
report **every** container of a pod - init and ephemeral ones included - and name
the role in a `KIND` column (`app`/`init`/`eph`), because an init container's
requests, images and security context count exactly as much as an app
container's. `reqlim`, `no-limits`, `no-requests`, `probes`, `qos` and
`unused-config` drop kube-system from the `-A` view only; an explicit
`-n kube-system` still returns its rows. The README's
[Container kinds](../README.md#container-kinds) and
[Security flags](../README.md#security-flags) sections are the reference for
the `KIND` and `FLAGS` values.

**Verdict commands** (compute a health classification, default-sorted worst-last)
- `pdb` - PodDisruptionBudget drain-safety verdict
- `pending` - Pending pods with a synthesized blocking reason
- `hpa` - HorizontalPodAutoscaler autoscaling verdict
- `spread` - replica placement single-point-of-failure verdict
- `probes` - readiness/liveness/startup probe reliability verdict
- `qos` - QoS class + eviction-risk verdict (`NO-MEM-FLOOR` is the finding the
  class itself hides: no memory request means eviction alongside `BestEffort`)
- `svc-backends` - services + ready/not-ready endpoint counts + wiring verdict
  (`NO-PODS` is a selector matching nothing, `UNWIRED` a service nothing can
  ever answer for); the selector is printed in full only on a `NO-PODS` row
- `rollouts` - Deployments/StatefulSets/DaemonSets and Argo Rollouts that are
  not finished rolling out (`STALLED` will not recover on its own,
  `NOT-OBSERVED` points at the controller, not the workload)
- `ingress` - ingress rules flattened per host+path, each checked against its
  backend service/port and its TLS secret
- `terminating` - pods and namespaces stuck being deleted, with the blocker
  (finalizer, unreachable node, namespace condition); cluster-wide

**Interactive**
- `secret` - browse secrets interactively (pick secret, then key); positional
  args skip the pickers. The only command that draws promptui pickers, and only
  when stdout is a TTY.

`kubectl klens --help` prints the same catalog (it is generated from the
registry, so it can't drift). Subcommands accept singular/plural aliases
(`image` ≡ `images`).

## Cross-cutting behaviour

These five behaviours apply across commands - learn them once.

### Namespace defaulting
Some commands default to the **current kubeconfig namespace** (the one set by
kubens/kubectx); the rest default to **all namespaces**.

- Current-namespace-by-default: `reqlim`, `no-limits`, `no-requests`, `images`,
  `restarts`, `pvc`, `svc-fqdn`, `svc-backends`, `secret`, `privileged`, `pdb`,
  `pending`, `hpa`, `spread`, `probes`, `qos`, `rollouts`, `ingress`,
  `pvc-unused`, `unused-config`.
- `-A` / `--all-namespaces` widens to all; `-n <ns>` targets one.
- `autoscaler` ignores namespace flags entirely (always `kube-system`).

This is driven by `Command.CurrentNSDefault` and is locked by a guard test -
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

### Request bounds and interruption
Every apiserver request is bounded by `--request-timeout` (default `1m0s`;
`--request-timeout=0` removes the bound). It is a safety net against an
unresponsive control plane, not a budget - the heaviest command measured on a
6500-pod cluster takes about four seconds. If you do hit it, the error names the
flag rather than failing opaquely:

```console
$ kubectl klens reqlim -A --request-timeout 300ms
error: request timed out after 300ms; raise it or pass --request-timeout=0 to disable
```

`Ctrl-C` (SIGINT) and SIGTERM cancel in-flight requests and exit `130` with a
plain `canceled`, so a cluster-wide listing stops when asked instead of first
running to completion.

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
make lint       # golangci-lint run (config: .golangci.yml)
make snapshot   # goreleaser release --snapshot --clean (dry-run)

go test -race ./internal/view -run TestNodes    # single test
```

`Taskfile.yml` mirrors the Makefile (`task build`, `task test`, ...), including
the bare invocation: `make` and `task` both just list the available targets.
`make lint` runs **golangci-lint** (`.github/workflows/lint.yml`, config in
`.golangci.yml`), the same linter CI enforces. The
`ci.yml` Test job runs `go mod verify`, build, and `go test -race`. Separate
hardening workflows add `govulncheck` (dependency CVEs), `zizmor` (GitHub Actions
security), goimports formatting, and markdownlint; Renovate keeps dependencies
current.

## Release

Releases are **automatic on push to `master`**. `.github/workflows/release.yml`
runs [`svu`](https://github.com/caarlos0/svu) to compute the next `vX.Y.Z` from
the conventional commits since the last tag (`feat` → minor, `fix` → patch,
`feat!`/`BREAKING CHANGE` → major); when `svu next` equals `svu current` nothing
is releasable and the job stops there. Otherwise it creates the tag with a single
`gh api` call - so the checkout keeps `persist-credentials: false` - and runs
goreleaser in the same job. A `GITHUB_TOKEN`-created tag does not re-trigger a
workflow, which is why the tagging and the release live in one job. So you
release by writing the right commit type, not by tagging. Pushing a `v*` tag by
hand still works as an escape hatch (the goreleaser steps also fire on
`ref_type == 'tag'`).

Two consequences worth knowing: **`perf:` does not cut a release** - svu
implements the Conventional Commits spec, where only `fix` and `feat` are
normative, and it has no setting to add keywords, so use `fix:` for a change that
has to ship on its own. And `--v0` keeps a breaking change from jumping straight
to `v1.0.0` while the project is pre-1.0.

goreleaser builds cross-platform archives and pushes the regenerated
`plugins/klens.yaml` to the central
[PixiBixi/krew-index](https://github.com/PixiBixi/krew-index) repo (the `krews`
publisher, using the `KREW_INDEX_TOKEN` PAT for the cross-repo push).
Version/commit/date are injected via `-X main.version=...` ldflags.

Every release also gets a **build-provenance attestation** over the archives and
`checksums.txt` (`actions/attest-build-provenance`, signed keylessly with the
job's `id-token`). A signature would say who published; provenance says *how* the
artifact was built - which repo, workflow and commit. Verify a download with:

```bash
gh attestation verify kubectl-klens_<version>_Darwin_arm64.tar.gz \
  --repo PixiBixi/kubectl-klens
```

Renovate drives the version bumps (`renovate.json`): minor Go-module updates map
to `feat(deps)` (minor release), patch/digest to `fix(deps)` (patch release),
and GitHub-Actions updates stay `chore(deps)` (**no** release - they don't ship
in the binary). Runner-label bumps ride that same rule: `github-runners` is a
Renovate *datasource*, not a manager, so the `github-actions` manager carries
them and listing it under `matchManagers` would be invalid. Minor/patch/digest
updates automerge via PR once CI passes, but
only after a **5-day `minimumReleaseAge` cooldown**: a hijacked package is
usually spotted and yanked within days, so waiting costs nothing and catches the
window that matters. Digest-only updates sit in the same hold, because a moved
tag means an upstream ref now points at a different commit.
`vulnerabilityAlerts` overrides the cooldown, so CVE fixes still land at once.
Actions are pinned to commit digests rather than tags
(`helpers:pinGitHubActionDigests`, `pinDigests`), so Renovate keeps the digests
moving instead of trusting a mutable tag. Two tools are pinned as plain
`*_VERSION` env vars that no built-in manager sees - svu in `release.yml` and
goimports (`golang.org/x/tools`) in `go-format.yml` - so a regex
`customManager` keyed on the `# renovate:` comment above each keeps those pins
current too.

## Where to go next

- **[architecture.md](architecture.md)** - the cli→view→kube layering, the
  `RunFunc` contract, the paged/pushed-down listing layer, the `Table`/`Painter`
  output mechanics, the shared view helpers, the verdict-command pattern, and a
  step-by-step guide to adding a subcommand.
