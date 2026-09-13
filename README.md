# kubectl-klens

<p align="center">
  Read-only cluster inspection for kubectl.<br>
  One binary, one dispatcher, 35 commands.
</p>

<p align="center">
  <a href="https://github.com/PixiBixi/kubectl-klens/releases"><img
    src="https://img.shields.io/github/v/release/PixiBixi/kubectl-klens?sort=semver&color=3b7dd8"
    alt="release"></a>
  <a href="https://github.com/PixiBixi/kubectl-klens/actions/workflows/ci.yml"><img
    src="https://img.shields.io/github/actions/workflow/status/PixiBixi/kubectl-klens/ci.yml?branch=master&label=ci"
    alt="ci"></a>
  <a href="https://github.com/PixiBixi/kubectl-klens/actions/workflows/lint.yml"><img
    src="https://img.shields.io/github/actions/workflow/status/PixiBixi/kubectl-klens/lint.yml?branch=master&label=lint"
    alt="lint"></a>
  <a href="https://github.com/PixiBixi/kubectl-klens/actions/workflows/govulncheck.yml"><img
    src="https://img.shields.io/github/actions/workflow/status/PixiBixi/kubectl-klens/govulncheck.yml?branch=master&label=govulncheck"
    alt="govulncheck"></a>
  <a href="https://goreportcard.com/report/github.com/PixiBixi/kubectl-klens"><img
    src="https://goreportcard.com/badge/github.com/PixiBixi/kubectl-klens"
    alt="go report card"></a>
  <a href="https://github.com/PixiBixi/kubectl-klens/blob/master/LICENSE"><img
    src="https://img.shields.io/github/license/PixiBixi/kubectl-klens?color=blue"
    alt="license"></a>
</p>

```console
$ kubectl klens max-pods
NODE                 MAXPODS  USED  FREE
pool-main-9f3c1a02   110      104   6
pool-main-1d77be54   110      61    49
pool-batch-4ab0c9e1  32       12    20
```

Commands accept their singular or plural form interchangeably (`kubectl klens
image` ≡ `kubectl klens images`, `node` ≡ `nodes`, ...).

Why each verdict says what it says is documented in
[openwiki/quickstart.md](openwiki/quickstart.md); the internals are in
[openwiki/architecture.md](openwiki/architecture.md).

## Contents

[Install](#install) -
[Commands](#commands) -
[Flags](#flags) -
[Namespace scope](#namespace-scope) -
[Container kinds](#container-kinds) -
[Security flags](#security-flags) -
[Certificate expiry](#certificate-expiry) -
[Sorting](#sorting) -
[Watch](#watch) -
[Color](#color) -
[Shell completion](#shell-completion) -
[Development](#development)

## Install

Published to the [PixiBixi krew custom index](https://github.com/PixiBixi/krew-index):

```bash
kubectl krew index add pixibixi https://github.com/PixiBixi/krew-index.git
kubectl krew install pixibixi/klens
kubectl krew upgrade klens   # later, to update
```

Or download a release archive, extract `kubectl-klens` onto your `PATH`, and
invoke it as `kubectl klens`. The darwin binaries require macOS 13 Ventura or
later (Go 1.27 toolchain minimum).

## Commands

`†` = defaults to the current kubeconfig namespace (kubens/kubectx); `-A` widens
to all namespaces, `-n <ns>` targets one. All other commands are node- or
cluster-scoped. See [Namespace scope](#namespace-scope).

### Nodes & capacity

<details>
<summary>10 commands</summary>

| Command | Shows |
| --- | --- |
| `nodes` | nodes + pool + instance-type + compute class + provisioning (spot/on-demand) |
| `taints` | taints per node |
| `capacity` | CPU/mem capacity + allocatable |
| `zones` | region/zone per node |
| `node-ips [node]` | internal + external IP and compute class per node, or for one node |
| `pods-per-node` | pod count per node |
| `max-pods` | pod ceiling, non-terminated count, free slots per node |
| `node-conditions` | node readiness + memory/disk/pid pressure |
| `on-node <node>` | pods on a node |
| `autoscaler` | cluster-autoscaler summary + per-nodegroup table (always `kube-system`) |

</details>

`nodes` reads its `NODEPOOL`, `CLASS` and `PROVISIONING` columns from provider
labels, so the same command works on GKE, EKS (managed node groups and
Karpenter) and AKS. `node-ips` reads `CLASS` from the same labels:

<details>
<summary>Provider labels read per column</summary>

| Column | Labels read |
| --- | --- |
| `NODEPOOL` | `cloud.google.com/gke-nodepool`, `eks.amazonaws.com/nodegroup`, `karpenter.sh/nodepool`, `kubernetes.azure.com/agentpool` |
| `CLASS` | `cloud.google.com/compute-class` (GKE-only by design; `<none>` elsewhere) |
| `PROVISIONING` | `cloud.google.com/gke-spot`, `cloud.google.com/gke-preemptible`, `cloud.google.com/gke-provisioning`, `eks.amazonaws.com/capacityType`, `karpenter.sh/capacity-type`, `kubernetes.azure.com/priority`, `kubernetes.azure.com/scalesetpriority` |

</details>

### Workloads & resources

<details>
<summary>8 commands</summary>

| Command | Shows |
| --- | --- |
| `reqlim` † | requests/limits per container (`-A` excl. kube-system) |
| `no-limits` † | containers missing CPU/mem limits |
| `no-requests` † | containers missing CPU/mem requests |
| `qos` † | QoS class + pod totals + eviction-risk verdict |
| `images` † | image per container per pod |
| `image-count` | image counts, split registry/image/tag (cluster-wide) |
| `unused-config` † | ConfigMaps/Secrets nothing references |
| `restarts` † | restarted containers + crash reason + last exit code + last restart time |

</details>

`unused-config` scans pod *templates* as well as live pods (volumes,
`env.valueFrom`, `envFrom`, `imagePullSecrets`, CSI secrets, ServiceAccount
secrets, Ingress TLS, container args), skips what the platform owns
(`kube-root-ca.crt`, SA tokens, Helm history, label-selected Grafana dashboards)
and names the owning controller. It cannot see an object a controller reads by
fixed name, so read it as a review queue, not a delete script.

`reqlim`, `no-limits`, `no-requests`, `probes`, `qos` and `unused-config` drop
kube-system from the `-A` view only; an explicit `-n kube-system` still returns
its rows.

`reqlim`, `no-limits`, `no-requests`, `images`, `probes` and `qos` accept
`--by-owner`, which reads the controllers behind the pods instead of the pods
themselves - Deployments, StatefulSets, DaemonSets, Argo Rollouts, Strimzi
PodSets and CloudNativePG Clusters: one row per container
per workload, with a `REPLICAS` column, and far cheaper on a large cluster - on
a 7209-pod cluster the controllers are 164 objects and ~700 kB against ~77 MB of
pods, about 4-7x faster end to end. Which controller kinds it reads, and what
the desired-spec view hides, is in
[openwiki/quickstart.md](openwiki/quickstart.md#by-owner---by-owner).

```console
$ kubectl klens qos -n 'app-a-*' --by-owner
NS                    WORKLOAD         REPLICAS  QOS         REQ_CPU  LIM_CPU  REQ_MEM  LIM_MEM  VERDICT
app-a-inference-prod  app-a-inference  1         Guaranteed  7        7        4G       4G       GUARANTEED
app-a-training-prod   app-a-training   1         Burstable   4        none     16G      none     BURSTABLE
```

### Storage & networking

<details>
<summary>6 commands</summary>

| Command | Shows |
| --- | --- |
| `pvc` † | PVCs bound to pod + node + storage class + size |
| `pvc-unused` † | PVCs no pod mounts + why they are still there |
| `pvc-resize` † | PVCs whose size does not match the request + why |
| `svc-fqdn` † | in-cluster FQDN of services |
| `svc-backends` † | services + the pods behind them + wiring verdict |
| `ingress` † | ingress rules flattened + backend/TLS checks |

</details>

### Security

<details>
<summary>3 commands</summary>

| Command | Shows |
| --- | --- |
| `default-sa` | pods still using the default service account |
| `privileged` † | containers with privileged/host security flags |
| `certs` † | TLS secrets + certificate expiry + renewal verdict |

</details>

### Reliability (verdicts)

<details>
<summary>7 commands</summary>

| Command | Shows |
| --- | --- |
| `pdb` † | PodDisruptionBudgets + drain-safety verdict |
| `pending` † | Pending pods + synthesized blocking reason |
| `hpa` † | HorizontalPodAutoscalers + current/target metrics + autoscaling verdict |
| `spread` † | replica placement across nodes/zones + SPOF verdict |
| `probes` † | readiness/liveness/startup probes + verdict |
| `terminating` | pods/namespaces stuck being deleted + blocker |
| `rollouts` † | workloads not finished rolling out (+ Argo Rollouts) |

</details>

Argo Rollouts are read through the dynamic client: no CRD or no RBAC on it gets
you the three built-in kinds and no `Rollout` rows, not an error.

### Secrets

<details>
<summary>4 forms</summary>

| Command | Shows |
| --- | --- |
| `secret` | pick a secret, then a key (interactive) |
| `secret <name>` | pick a key of `<name>` (interactive) |
| `secret <name> <key>` | decode and print one key's value |
| `secret <name> all` | decode and print all keys |

</details>

Pickers only in a terminal; piped (script, CI) it falls back to plain listings.
In a picker, `/` filters as you type. A single-key secret skips the key picker.

## Flags

`--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`, `--color`,
`--request-timeout`, `--version`.

`--request-timeout` bounds each apiserver request (default `1m0s`) so an
unresponsive control plane can't hang the command forever; `--request-timeout=0`
removes the bound. `--sort`, `-w/--watch`, `--interval` and `--by-owner` are
per-command flags (`<TAB>` lists what a given command takes). `Ctrl-C` cancels
in-flight requests and exits `130`, or `0` under `--watch`.

## Namespace scope

The `†` commands default to the current kubeconfig namespace (kubens/kubectx);
`-A` widens to all namespaces and `-n` targets one. The other pod-scoped
commands (including `image-count`) default to all namespaces. `autoscaler`
always reads `kube-system` and ignores namespace flags.

`-n` is checked against the cluster before the command runs: an unknown
namespace is an error, not an empty table.

```console
$ kubectl klens restarts -n nosuchns
error: namespace "nosuchns" not found
```

`-n` also takes a **shell glob** (`*`, `?`, `[abc]`), not a regexp: `.` is a
literal character, so `app.*` matches nothing and `app-*` is what you want (klens
says so when a pattern looks like a regexp). Quote it, or your shell may expand
it against the current directory:

```bash
kubectl klens restarts -n 'app-*'      # every app-* namespace
kubectl klens reqlim -n 'app-[dp]*'
```

A pattern matching no namespace is an error too. Expanding one needs
cluster-wide `list` rights on namespaces; a plain `-n <name>` only needs `get`
on that namespace.

`kubectl klens restarts -n <TAB>` completes namespace names from the cluster.

## Container kinds

`reqlim`, `no-limits`, `no-requests`, `images`, `restarts` and `privileged`
report **every** container of a pod, not just the app ones, and name the role in
a `KIND` column:

<details>
<summary>The three KIND values</summary>

| KIND | Container |
| --- | --- |
| `app` | a regular `spec.containers` entry |
| `init` | a `spec.initContainers` entry (including native sidecars) |
| `eph` | an ephemeral debug container (`kubectl debug`) |

</details>

## Security flags

`privileged` reports these tokens in its `FLAGS` column:

<details>
<summary>The nine FLAGS tokens</summary>

| Flag | Meaning |
| --- | --- |
| `privileged` | `securityContext.privileged: true` |
| `privesc` | `allowPrivilegeEscalation: true`, set explicitly |
| `privesc-default` | `allowPrivilegeEscalation` unset - resolved to **true** |
| `caps=A+B` | added host-level capabilities (`SYS_ADMIN`, `BPF`, …) |
| `root` | `runAsUser: 0`, from the container or pod security context |
| `hostPort` | a container port bound on the node |
| `hostNetwork`, `hostPID`, `hostIPC` | pod sharing that host namespace |
| `hostPath` | pod mounting a host directory |

</details>

## Certificate expiry

`certs` lists every `kubernetes.io/tls` secret with the date its certificate
stops working, how long that is from now, and a verdict. Why the date is the
earliest in the chain and how `NAMES` groups hosts is in the
[command catalog](openwiki/quickstart.md#command-catalog).

```console
$ kubectl klens certs -A --sort in
NS          SECRET         NAMES                              ISSUER         NOT_AFTER         IN   VERDICT
video-prod  video-app-tls  example.com (74), example.net (4)  Let's Encrypt  2026-10-09 08:35  33d  OK
```

| verdict | when |
| --- | --- |
| `EXPIRED` | already past |
| `EXPIRING` | under 7 days |
| `RENEW-DUE` | under 14 days |
| `OK` | beyond that |
| `INVALID` | `tls.crt` missing or unparseable |

## Sorting

Most table commands accept `--sort <column>` (e.g. `kubectl klens nodes --sort
nodepool`). Sorting is ascending, numeric columns by value; `<TAB>` completes
the valid names per command.

Defaults that differ: `image-count` and `restarts` sort count-descending,
`autoscaler` by `LAST-CHANGE` descending, and verdict commands (`pdb`, `hpa`,
`spread`, `probes`, `qos`, `svc-backends`, `rollouts`, `ingress`, `terminating`,
`pvc-unused`, `pvc-resize`) by `VERDICT` severity least-risky first, so the
riskiest rows land nearest the prompt.

## Watch

Nine commands accept `-w/--watch`, which re-runs them every `--interval`
(default `2s`, floor `1s`) and redraws the screen:

```console
$ kubectl klens pending -A --watch
Every 2s: klens pending -A --watch   14:03:22 (Ctrl-C to stop)

NS     POD             REASON
prod   api-7f9c-x2k    Insufficient cpu
```

`pending`, `restarts`, `rollouts`, `terminating`, `autoscaler`,
`node-conditions`, `svc-backends`, `max-pods`, `pvc-resize`.

## Color

<details>
<summary>What each color means</summary>

| Color | Meaning |
| --- | --- |
| green | good - Ready/Healthy/Bound/Running, roomy free pod slots, `on-demand` nodes, readable HPA metrics |
| yellow | warning - Pending, high restart counts, floating `latest` tags, <25% free pod slots, `NoSchedule` taints, `spot`/`preemptible` nodes |
| red | bad - NotReady/Unknown/CrashLoopBackOff, node pressure, privileged flags, <10% free pod slots, `NoExecute` taints, `<unknown>` HPA metrics |
| gray | muted placeholders - `<none>`/`none`, `PreferNoSchedule` taints |
| bold | headers |

</details>

<details>
<summary>Verdict coloring per command</summary>

| Command | green | yellow | red | gray |
| --- | --- | --- | --- | --- |
| `pdb` | `OK` | `AT-FLOOR` | `BLOCKED`/`PERMABLOCK`/`NO-GUARD` | `ORPHAN` |
| `hpa` | `OK` | `SCALING` | `MAXED`/`NO-METRICS` | `AT-MIN` |
| `spread` | `SPREAD` | `SPOF-ZONE` | `SPOF-NODE` | `SINGLE`/`MULTI-NODE` |
| `probes` | `OK` | `NO-LIVENESS` | `NO-READINESS`/`NO-PROBES` | - |
| `qos` | `GUARANTEED` | `BURSTABLE` | `NO-MEM-FLOOR`/`EVICT-FIRST` | - |
| `svc-backends` | `OK` | `DEGRADED` | `NO-PODS`/`NO-READY`/`UNWIRED` | `EXTERNAL`/`MANUAL` |
| `rollouts` | `OK` | `PROGRESSING`/`PAUSED` | `STALLED`/`DOWN`/`NOT-OBSERVED` | `SCALED-ZERO` |
| `ingress` | `OK` | `NO-TLS` | `NO-SERVICE`/`NO-PORT`/`NO-SECRET` | `RESOURCE` |
| `terminating` | - | `DELETING` | `STUCK` | `GRACE` |
| `pvc-unused` | - | `STS-RESERVED`/`SCALED-DOWN` | `ORPHAN`/`LOST` | `UNBOUND` |
| `pvc-resize` | - | `PENDING`/`RESIZING`/`FS-PENDING`/`SHRINK` | `FAILED`/`INFEASIBLE`/`SC-NO-EXPAND` | - |

</details>

Control it with `--color=auto|always|never` (default `auto`, which colors only
when stdout is a terminal). `NO_COLOR` disables color; `KLENS_COLOR` sets the
default via the environment. **Under kubecolor** klens' stdout is a pipe, so
`auto` turns color off; force it with `--color=always`.

## Shell completion

`kubectl klens <TAB>` uses kubectl's plugin-completion mechanism (kubectl 1.26+):
kubectl looks for an executable `kubectl_complete-klens` on your `PATH`. Load
kubectl's own completion first (e.g. `source <(kubectl completion zsh)`), then
let klens drop the shim into krew's bin dir:

```bash
kubectl klens completion install
kubectl klens completion install --dir /usr/local/bin   # non-krew install
```

Standalone, drop both executables from the extracted archive on your `PATH`:

```bash
install -m 0755 kubectl-klens /usr/local/bin/
install -m 0755 completion/kubectl_complete-klens /usr/local/bin/
```

## Development

```bash
make test      # go test -race ./...
make lint      # golangci-lint run
make build     # local binary
make snapshot  # goreleaser dry-run
```
