# kubectl-klens - Quickstart

`kubectl-klens` is a single-binary **kubectl plugin** (`kubectl klens`) bundling
~34 read-only cluster-inspection shortcuts behind one dispatcher. It is the
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
- `nodes` - nodes + pool + instance-type + compute class + provisioning
  (spot/on-demand, from GKE, EKS/Karpenter and AKS labels)
- `taints` - taints per node
- `capacity` - CPU/mem capacity + allocatable per node
- `zones` - region/zone per node
- `node-ips [node]` - internal + external IP and compute class per node, or for a
  single node. `CLASS` comes from the same provider labels `nodes` reads, so an
  address can be tied to the pool shape it came from in one command. The two
  IP columns read differently on purpose: a missing `INTERNAL-IP` is red (the
  control plane has no route to that kubelet), a missing `EXTERNAL-IP` is a muted
  `<none>` (the wanted state on private nodes), and a public address is yellow
  because it is internet-reachable surface. A dual-stack node shows both of its
  addresses comma-joined (`10.0.0.4,fd00::4`)
- `pods-per-node` - pod count per node
- `max-pods` - pod ceiling (allocatable), current count, free slots per node
- `node-conditions` - readiness + memory/disk/pid pressure
- `on-node <node>` - pods scheduled on a given node
- `autoscaler` - cluster-autoscaler status (always reads `kube-system`), rendered
  from the structured-YAML status (CA 1.30+) or the older legacy text, falling
  back to the raw value when neither parses. `LAST-CHANGE` is the nodegroup's most
  recent condition `lastTransitionTime` and is populated **only** by the
  structured format - it stays empty against a legacy-format autoscaler

**Workload hygiene (namespace-scoped)**
- `reqlim` - requests/limits per container
- `no-limits` / `no-requests` - containers missing limits / requests
- `images` - image per container per pod
- `image-count` - image occurrence counts split registry/image/tag (cluster-wide)
- `unused-config` - ConfigMaps/Secrets nothing references (pod templates and
  container args included, so a CronJob between runs and a
  `--configmap=ns/name` flag are not false positives), biggest first, with the
  owning controller named - an object owned by an `ExternalSecret` or a `Kafka`
  CR is reviewed there, not deleted here
- `restarts` - restarted containers + crash reason + last exit code (137/143 =
  SIGKILL/SIGTERM) + the local time of the last restart (`LAST`)
- `qos` - QoS class + pod requests/limits totals + eviction-risk verdict
- `pvc` - PVCs bound to pod + node, with the claim's storage class (`<default>`
  when the claim leaves `storageClassName` unset) and its provisioned size
  (`CAPACITY` falls back to the requested size while the claim is unbound). Fill
  rate is out of scope: it lives in the kubelet volume stats, not the typed API -
  use [`df-pv`](https://github.com/yashbhutwala/kubectl-df-pv) for that
- `pvc-unused` - PVCs no pod mounts, with capacity and storage class. `ORPHAN` is
  the reclaimable one (no pod, no owner that could mount it again),
  `SCALED-DOWN` a StatefulSet leftover that scaling back up would reuse, and
  `STS-RESERVED` a slot inside the set's replica count whose pod is expected
  back. A claim held by a still-terminating pod counts as in use
- `pvc-resize` - PVCs whose capacity does not match the request, with where the
  resize stalled: `SC-NO-EXPAND` never starts (the StorageClass forbids
  expansion, and only a new PVC gets you out), `INFEASIBLE` is past the disk
  type's ceiling, `FS-PENDING` waits on a node remount and the `POD` column names
  the pod to restart, `SHRINK` is a silent no-op because Kubernetes cannot shrink
  a claim. Claims already at their requested size are not listed, so an empty
  table means nothing is in flight or stuck
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
- `hpa` - HorizontalPodAutoscaler current/target metrics + autoscaling verdict.
  `TARGETS` mirrors `kubectl get hpa` (`cpu: 67%/70%`, one cell per metric
  source, utilization as a percentage and value targets as quantities) so the
  number the autoscaler reacts to sits next to the verdict. A metric with no
  reported current value reads `<unknown>` in red: that is `NO-METRICS` seen per
  metric rather than per HPA. `<auto>` is a target the spec never set,
  `<none>` an HPA with no metrics at all. The verdict itself is driven by the
  replica counts and the `ScalingActive` condition: `MAXED` is pinned at the
  ceiling with no headroom left, `AT-MIN` is idle at the floor (muted, not a
  finding)
- `spread` - replica placement single-point-of-failure verdict
- `probes` - readiness/liveness/startup probe reliability verdict
- `qos` - QoS class + eviction-risk verdict (`NO-MEM-FLOOR` is the finding the
  class itself hides: no memory request means eviction alongside `BestEffort`)
- `svc-backends` - services + ready/not-ready endpoint counts (from the
  EndpointSlices) + wiring verdict. `NO-PODS` is a selector matching nothing,
  `NO-READY` a workload whose pods all fail readiness; a selector-less service is
  `UNWIRED` when nothing filled its endpoints in and `MANUAL` when another
  controller did, and an `ExternalName` service is a DNS alias, so it stays muted
  as `EXTERNAL`. The `SELECTOR` column is spelled out only on a `NO-PODS` row,
  where the typo is what you came to read - elsewhere it is a label count,
  because the usual three-key Helm selector spends 110 columns saying nothing and
  wraps the row. Endpoints are deduplicated per pod, so a dual-stack service (one
  EndpointSlice per address family) is not double-counted
- `rollouts` - Deployments/StatefulSets/DaemonSets and Argo Rollouts that are
  not finished rolling out (`STALLED` will not recover on its own,
  `NOT-OBSERVED` points at the controller, not the workload)
- `ingress` - ingress rules flattened to one row per host+path, each checked
  against the cluster: the backend service exists (`NO-SERVICE`) and exposes the
  port the rule names, by number or by name (`NO-PORT`), and the host is covered
  by a TLS block whose secret is actually there (`NO-SECRET` - the controller
  falls back to its own certificate, so browsers see a name mismatch while
  `get ing` looks fine). A host on plaintext only is `NO-TLS`. Wildcard
  certificates cover one label, as TLS name matching does. Listing secrets is a
  privilege many users lack, so a refused list downgrades the `TLS` column to an
  unverified (muted) name rather than failing the command
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

These behaviours apply across commands - learn them once.

### Namespace defaulting
Some commands default to the **current kubeconfig namespace** (the one set by
kubens/kubectx); the rest default to **all namespaces**.

- Current-namespace-by-default: `reqlim`, `no-limits`, `no-requests`, `images`,
  `restarts`, `pvc`, `svc-fqdn`, `svc-backends`, `secret`, `privileged`, `pdb`,
  `pending`, `hpa`, `spread`, `probes`, `qos`, `rollouts`, `ingress`,
  `pvc-unused`, `pvc-resize`, `unused-config`.
- `-A` / `--all-namespaces` widens to all; `-n <ns>` targets one.
- `nodes`, `taints`, `capacity`, `zones`, `node-ips`, `max-pods`,
  `node-conditions` and `autoscaler` read only cluster-scoped objects and ignore
  namespace flags entirely.

This is driven by `Command.CurrentNSDefault` and `Command.IgnoresNamespace`, both
locked by guard tests - see
[architecture.md](architecture.md#namespace-defaulting).

### Namespace validation and globs (`-n`)
`-n` is resolved against the cluster before the command runs, so a typo fails
loudly instead of printing an empty table:

```console
$ kubectl klens restarts -n be-znoff
error: namespace "be-znoff" not found
```

It also accepts a `path.Match` glob (`*`, `?`, `[abc]`) - a shell glob, not a
regexp, so `.` is literal and `be.*` matches nothing. The error says so when a
pattern looks like a regexp, on both the no-match and the not-found path.
The glob is expanded into one List per matched namespace. Quote it so the shell
does not expand it first:

```bash
kubectl klens restarts -n 'be-*'
```

A pattern matching nothing is an error too. Expansion needs cluster-wide `list`
on namespaces; a literal `-n <name>` only needs `get` on that one.
`kubectl klens restarts -n <TAB>` completes namespace names from the cluster -
the only completion that talks to a cluster, and it stays silent on any failure.

### By owner (`--by-owner`)
`reqlim`, `no-limits`, `no-requests`, `images`, `probes` and `qos` set
`ByOwner: true` in the registry and accept `--by-owner`. It is a source switch,
not a dedup: with the flag, `podsForView` (`internal/view/byowner.go`) lists
Deployments, StatefulSets, DaemonSets and Argo Rollouts and turns each into a
synthetic pod - Namespace/Name from the controller, Spec its pod template,
Status left zero - which then flows through the view's normal per-container
loop unmodified. The pod-identity column becomes `WORKLOAD` and a `REPLICAS`
column appears, free at that point since the controller list already carries
the count.

```bash
kubectl klens qos -n prod --by-owner
```

This is what makes the flag pay for itself on a large cluster: on a 7209-pod
GKE cluster the four controller lists are 164 objects and ~700 kB, against
~77 MB for every pod - about 4-7x faster end to end. The trade
is what the row means: with the flag it is the **desired** spec (a rollout in
flight shows only the new one, a pod mutated after creation is invisible),
without it it is what is actually **running**. None of the six views reads
`pod.Status` except `qos`, which already has a from-spec fallback for exactly
this case - that is what let this be one code path instead of two. `--sort pod`
and `--sort workload` are both accepted in both modes.

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

### Watch (`-w/--watch`)
A command that sets `Watch: true` in the registry opts into `-w/--watch`, which
re-polls it every `--interval` (default `2s`, floor `1s`) and redraws the screen
with a status line above the table. It is deliberately not the whole catalog: the
opted-in commands are the ones whose answer changes while you look at it -
`pending`, `restarts`, `rollouts`, `terminating`, `autoscaler`,
`node-conditions`, `svc-backends`, `max-pods`, `pvc-resize`. Passing it
elsewhere is refused by name (`error: nodes does not support --watch`).

```console
$ kubectl klens pending -A --watch --interval 5s
Every 5s: klens pending -A --watch --interval 5s   14:03:22 (Ctrl-C to stop)

NS     POD             REASON
prod   api-7f9c-x2k    Insufficient cpu
```

It is a re-poll, not a Kubernetes watch stream: each tick re-runs the view's
list calls, which is why the interval has a floor. A failed poll prints its
error and the loop continues (a transient `503` mid-rollout is not a reason to
drop out), `Ctrl-C` exits `0` and leaves the last frame on screen, and a
non-TTY stdout refuses the flag instead of writing clear-screen escapes into a
pipe.

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
running to completion. Under `--watch` the same signal is the intended way out,
so it exits `0` and prints nothing.

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
make build      # go build -ldflags "-s" -o kubectl-klens .
make test       # go test -race ./...
make bench      # go test -bench (BENCH=<re> COUNT=<n> BENCHTIME=<n>)
make lint       # golangci-lint run (config: .golangci.yml)
make snapshot   # goreleaser release --snapshot --clean (dry-run)

go test -race ./internal/view -run TestNodes    # single test
```

`Taskfile.yml` mirrors the Makefile (`task build`, `task test`, ...), including
the bare invocation: `make` and `task` both just list the available targets.
`make lint` runs **golangci-lint** (`.github/workflows/lint.yml`, config in
`.golangci.yml`), the same linter CI enforces. The
`ci.yml` Test job runs `go mod verify`, build, and `go test -race`. Separate
workflows add `govulncheck` (dependency CVEs), `zizmor` (GitHub Actions
security), CodeQL (weekly plus every push/PR), dependency review on PRs
(`fail-on-severity: high`), an OpenSSF Scorecard run whose SARIF lands in the
security tab, goimports formatting, markdownlint, and a conventional-commit
check on the PR **title** - that title becomes the squash-merge commit subject,
which is what svu reads to decide the next version. Renovate keeps dependencies
current.

Every job starts with `step-security/harden-runner` in **audit** mode, never
block: GitHub's own egress endpoints move between runs, so an allowlist would
fail builds on infrastructure changes rather than on attacks. Write permissions
are declared per job (`permissions: {}` at the workflow level), so a workflow's
non-publishing jobs cannot reach the token that publishes.

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

Each release carries three supply-chain artifacts, all produced in that same
job. **SBOMs**: goreleaser shells out to syft over each archive (`sboms:` in
`.goreleaser.yml`), which is why the workflow installs syft up front - a missing
syft fails the release *after* the archives are built. A **cosign signature**
over `checksums.txt` (`signs:`), keyless, so the identity is the workflow rather
than a key; cosign v3 emits a single `.sigstore.json` bundle instead of the old
`.sig`/`.pem` pair. And a **build-provenance attestation** over the archives,
`checksums.txt` and the SBOMs (`actions/attest-build-provenance`, signed with
the job's `id-token`). The signature says who published; provenance says *how*
the artifact was built - which repo, workflow and commit. Signing
`checksums.txt` alone is enough because it pins the SHA256 of every archive.

Verify a download:

```bash
cosign verify-blob \
  --certificate-identity-regexp '^https://github.com/PixiBixi/kubectl-klens/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --bundle checksums.txt.sigstore.json checksums.txt
shasum -a 256 --check --ignore-missing checksums.txt
gh attestation verify kubectl-klens_<version>_Darwin_arm64.tar.gz \
  --repo PixiBixi/kubectl-klens
```

`SECURITY.md` is the user-facing copy of those commands.

A second **`verify` job** replays exactly that sequence against the *published*
release: it downloads the archives from the GitHub release, checks the
checksums, verifies the cosign bundle and verifies the provenance of every
archive. Signing is only worth having if it verifies, and without this job a
broken signing config ships in silence and the first person to hit it is a user.
The load-bearing part is the pinned `--certificate-identity-regexp`: a keyless
signature with an unchecked identity proves only that *somebody* signed. The job
is gated on `needs.release.outputs.tag != ''`, so a push that produced no
release skips it.

The release job also installs **cosign** before goreleaser-action runs, for a
second reason: the action verifies the cosign signature of the goreleaser binary
it downloads, and silently skips that check when cosign is absent from `PATH`.

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
- **[performance.md](performance.md)** - where the time actually goes (the
  apiserver, not Go), how to run and read the benchmarks, the lazy/narrowed list
  pattern that pays, and the optimisations already measured and rejected.
