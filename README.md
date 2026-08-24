# kubectl-klens

A kubectl plugin for quick, read-only cluster inspection. One dispatcher,
~25 shortcuts.

Commands accept their singular or plural form interchangeably (`kubectl klens
image` ≡ `kubectl klens images`, `node` ≡ `nodes`, ...).

## Install

Published to the [PixiBixi krew custom index](https://github.com/PixiBixi/krew-index):

```bash
kubectl krew index add pixibixi https://github.com/PixiBixi/krew-index.git
kubectl krew install pixibixi/klens
kubectl krew upgrade klens   # later, to update
```

Or download a release archive, extract `kubectl-klens` onto your `PATH`, and
invoke it as `kubectl klens`.

The darwin binaries require macOS 13 Ventura or later (Go 1.27 toolchain
minimum).

## Commands

`†` = defaults to the current kubeconfig namespace (kubens/kubectx); `-A` widens
to all namespaces, `-n <ns>` targets one. All other commands are node- or
cluster-scoped. See [Namespace scope](#namespace-scope) for details.

### Nodes & capacity

| Command | Shows |
| --- | --- |
| `nodes` | nodes + GKE nodepool + instance-type |
| `taints` | taints per node |
| `capacity` | CPU/mem capacity + allocatable |
| `zones` | region/zone per node |
| `node-ips [node]` | internal + external IP per node, or for one node |
| `pods-per-node` | pod count per node |
| `max-pods` | pod ceiling, non-terminated count, free slots per node |
| `node-conditions` | node readiness + memory/disk/pid pressure |
| `on-node <node>` | pods on a node |

The `STATUS` column of `nodes` and `node-conditions` separates the two ways a
node can be unhealthy: `NotReady` is a kubelet that is answering and reporting
itself unready, while `Unknown` is a kubelet that has **stopped reporting
altogether** - the state that starts the eviction clock once the `unreachable`
toleration expires. Both are red.

`node-ips` reads `.status.addresses` - the data you would otherwise dig out with
a `-o jsonpath` filter on `@.type=="ExternalIP"` - and reports the internal
address next to it. Passing a node name (`kubectl klens node-ips <node>`) narrows
the listing to that node; the filter is a `metadata.name` field selector, so the
apiserver returns that one node instead of the whole fleet, and an unknown name
is an error rather than an empty table. A dual-stack node shows both of its
addresses comma-joined (`10.0.0.4,fd00::4`). Color reads the two columns
differently: a missing
`INTERNAL-IP` is red (the control plane has no route to that kubelet), while a
missing `EXTERNAL-IP` is a muted `<none>` - the normal, wanted state on private
nodes. A public address is yellow, because it is internet-reachable surface.

### Workloads & resources

| Command | Shows |
| --- | --- |
| `reqlim` † | requests/limits per container (`-A` excl. kube-system) |
| `no-limits` † | containers missing CPU/mem limits |
| `no-requests` † | containers missing CPU/mem requests |
| `qos` † | QoS class + pod totals + eviction-risk verdict |
| `images` † | image per container per pod |
| `image-count` | image counts, split registry/image/tag (cluster-wide) |
| `unused-config` † | ConfigMaps/Secrets nothing references |
| `restarts` † | restarted containers + crash reason + last exit code |

### Storage & networking

| Command | Shows |
| --- | --- |
| `pvc` † | PVCs bound to pod + node |
| `pvc-unused` † | PVCs no pod mounts + why they are still there |
| `svc-fqdn` † | in-cluster FQDN of services |
| `svc-backends` † | services + the pods behind them + wiring verdict |
| `ingress` † | ingress rules flattened + backend/TLS checks |

`pvc-unused` is the FinOps counterpart of `pvc`: disks that are provisioned and
billed with nothing reading or writing them. A cloud-side audit cannot find
these - the disk is attached to a live PV that a live PVC references, so the
provider sees it as in use; only joining on the pod side shows nothing mounts
it. `ORPHAN` is the one to reclaim (no pod, no owner that could mount it again),
`SCALED-DOWN` is a StatefulSet leftover that scaling back up would reuse, and
`STS-RESERVED` is a slot inside the set's replica count whose pod is expected
back. A claim held by a pod that is still terminating counts as in use.

`svc-backends` answers "is anything actually behind this service": it counts the
ready and not-ready endpoints per service from its EndpointSlices and prints the
selector next to them, so a mistyped label (`NO-PODS`) or a workload whose pods
all fail readiness (`NO-READY`) reads as a fault instead of an empty
`get endpoints`. A selector-less service is `UNWIRED` when nothing filled its
endpoints in and `MANUAL` when another controller did; `ExternalName` is a DNS
alias and stays muted. Endpoints are counted per pod, so a dual-stack service
(one EndpointSlice per address family) is not double-counted.

`ingress` flattens every rule to one row per host+path and checks it against the
cluster: that the backend service exists (`NO-SERVICE`) and exposes the port the
rule names (`NO-PORT`, by number or by name), and that the host is covered by a
TLS block whose secret is actually there (`NO-SECRET` - the controller falls back
to its own certificate, so browsers see a name mismatch while `get ing` looks
fine). A host on plaintext only is `NO-TLS`. Wildcard certificates cover one
label, as TLS name matching does. Reading secrets is a privilege many users lack:
a refused secret list downgrades the `TLS` column to an unverified (muted) name
rather than failing the command.

### Security

| Command | Shows |
| --- | --- |
| `default-sa` | pods still using the default service account |
| `privileged` † | containers with privileged/host security flags |

### Reliability (verdicts)

| Command | Shows |
| --- | --- |
| `pdb` † | PodDisruptionBudgets + drain-safety verdict |
| `pending` † | Pending pods + synthesized blocking reason |
| `hpa` † | HorizontalPodAutoscalers + autoscaling verdict |
| `spread` † | replica placement across nodes/zones + SPOF verdict |
| `probes` † | readiness/liveness/startup probes + verdict |
| `terminating` | pods/namespaces stuck being deleted + blocker |
| `rollouts` † | workloads not finished rolling out (+ Argo Rollouts) |

`unused-config` lists ConfigMaps and Secrets nothing points at. The reference
scan is the whole value of it, so it reads **pod templates** as well as live
pods - a CronJob between runs and a workload scaled to zero own no pods, and
calling their config unused would be wrong - covering volumes (projected sources
included), `env.valueFrom`, `envFrom`, `imagePullSecrets`, CSI node-publish
secrets, the secrets a ServiceAccount holds, and Ingress TLS certificates. It
ignores what the platform owns: the `kube-root-ca.crt` ConfigMap,
`kubernetes.io/service-account-token` secrets and `helm.sh/release.v1` release
history. Rows are biggest first, since size is what decides which leftover to
delete first. It skips kube-system in the `-A` view.

One reference it cannot see: an object read through the API by an operator
rather than mounted (an external-secrets `SecretStore`, a controller reading a
ConfigMap by name). Check before deleting.

`qos` rolls the per-container view up to the pod: it prints the class the
apiserver assigned (`Guaranteed`/`Burstable`/`BestEffort`) next to what the pod
actually reserves in steady state - app containers plus native sidecars (init
containers with `restartPolicy: Always`), since a plain init container's spike
ends before the pod is up. The verdict adds what the class alone hides: a
`Burstable` pod with **no memory request** (`NO-MEM-FLOOR`) is ranked with the
`BestEffort` ones when the kubelet starts evicting on memory pressure, however
small its real footprint. It skips kube-system in the `-A` view, like `reqlim`
and `no-limits`.

`probes` skips kube-system in the `-A` view, like `reqlim` and `no-limits`.

`rollouts` answers "is everything finished deploying": one row per Deployment,
StatefulSet, DaemonSet and - when the CRD is installed - Argo Rollout, with the
desired/ready/updated/available counts and a verdict. `STALLED` is the one to
look for: a Deployment whose `Progressing` condition went `False`
(`ProgressDeadlineExceeded`) or a Rollout Argo called `Degraded` will not
recover on its own. `NOT-OBSERVED` means the controller has not acted on the
current spec yet, which points at a wedged or missing controller rather than at
the workload. For a canary the `STATE` column carries the step it sits on
(`Paused 2/5`).

`terminating` is `pending` at the other end of a resource's life: everything
carrying a `deletionTimestamp` that is still there, plus namespaces in the
`Terminating` phase, with the blocker named. The three that matter are a
finalizer nobody will clear, a node whose kubelet stopped answering (the pod
cannot be confirmed dead, so it hangs until force-deleted), and, for a
namespace, the condition the controller records and `get ns` never prints
(`NamespaceContentRemaining`, `NamespaceFinalizersRemaining`,
`NamespaceDeletionDiscoveryFailure`). A deletion inside its grace period is
`GRACE`; past five minutes it is `STUCK`, which is well beyond any sane
`terminationGracePeriodSeconds`. Cluster-wide by default, since a stuck
namespace is not scoped to one.

Argo Rollouts are read through the dynamic client. A cluster without the CRD, or
a user without RBAC on it, gets the three built-in kinds and no `Rollout` rows -
not an error, since that is the normal case on most clusters.

### Cluster autoscaler

| Command | Shows |
| --- | --- |
| `autoscaler` | cluster-wide summary + per-nodegroup table |

### Secrets

| Command | Shows |
| --- | --- |
| `secret` | pick a secret, then a key (interactive) |
| `secret <name>` | pick a key of `<name>` (interactive) |
| `secret <name> <key>` | decode and print one key's value |
| `secret <name> all` | decode and print all keys |

`secret` opens interactive pickers when run in a terminal; when piped (script,
CI) it falls back to plain listings (`secret` lists secrets, `secret <name>`
lists keys). In a picker, press `/` to filter as you type. A secret with a
single key skips the key picker and decodes that key directly.

## Flags

`--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`, `--color`,
`--request-timeout`, `--version`.

`--request-timeout` bounds each apiserver request (default `1m0s`) so an
unresponsive control plane can't hang the command forever. It is a safety net,
not a budget: the heaviest command measured on a 6500-pod cluster takes about
four seconds. Pass `--request-timeout=0` to remove the bound; if you hit it, the
error names the flag.

`Ctrl-C` cancels in-flight requests and exits `130`, so a cluster-wide listing
stops as soon as you ask rather than running to completion first.

## Namespace scope

The `†` commands above default to the current kubeconfig namespace (the one set
by kubens/kubectx); `-A` widens to all namespaces and `-n` targets a specific
one. The other pod-scoped commands (including `image-count`) default to all
namespaces.

`autoscaler` always reads from `kube-system` and ignores namespace flags. It
renders the cluster-autoscaler status (both the structured-YAML format from
CA 1.30+ and the older legacy text) into a cluster-wide summary plus a
per-nodegroup table, falling back to the raw status when neither format is
recognized. The table's `LAST-CHANGE` column shows each nodegroup's most recent
`lastTransitionTime` (across its health/scale-up/scale-down conditions), so a
recent scaling event is easy to spot - it is only populated from the
structured-YAML format.

## Container kinds

`reqlim`, `no-limits`, `no-requests`, `images`, `restarts` and `privileged`
report **every** container of a pod, not just the app ones, and name the role in
a `KIND` column:

| KIND | Container |
| --- | --- |
| `app` | a regular `spec.containers` entry |
| `init` | a `spec.initContainers` entry (including native sidecars) |
| `eph` | an ephemeral debug container (`kubectl debug`) |

This matters because init containers are not second-class: their requests count
toward the pod's scheduling footprint and toward `ResourceQuota`, their images
are pulled and executed on the node, a privileged one escalates exactly as far
as an app container, and one looping in `CrashLoopBackOff` wedges the whole pod.
Rows are emitted in startup order (init, then app, then ephemeral) and the
container name is printed verbatim, so it can be pasted straight into
`kubectl logs -c <name>`. `--sort kind` groups by role. `image-count` folds all
three kinds into its totals (it has no per-container column).

## Security flags

`privileged` reports these tokens in its `FLAGS` column:

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

`privesc-default` is only ever reported **alongside** another finding, never on
its own: nearly every container in a normal cluster leaves the field unset, so
triggering a row on it would bury the real findings instead of surfacing them.

## Sorting

Most table commands accept `--sort <column>` to order rows by one of their
columns (e.g. `kubectl klens zones --sort region`, `kubectl klens nodes --sort
nodepool`). Sorting is ascending, with numeric columns ordered by value.
`<TAB>` completes the valid column names per command.

Defaults that differ from ascending:

- `image-count` and `restarts` - count-descending.
- `autoscaler` - `LAST-CHANGE` descending (most recently changed nodegroup
  first). Sortable columns: `nodegroup|health|ready|target|min|max|scaleup|scaledown|last-change`.
- Verdict commands (`pdb`, `hpa`, `spread`, `probes`, `qos`, `svc-backends`,
  `rollouts`, `ingress`, `terminating`, `pvc-unused`) - `VERDICT` by severity
  (least-risky first), so the riskiest rows land at the bottom, nearest the
  prompt.

Pass `--sort <column>` to override any of these. `image-count` sortable columns:
`count|registry|image|tag`.

## Color

klens colorizes its tables:

| Color | Meaning |
| --- | --- |
| green | good - Ready/Healthy/Bound/Running, roomy free pod slots |
| yellow | warning - Pending, high restart counts, floating `latest` tags, <25% free pod slots, `NoSchedule` taints |
| red | bad - NotReady/Unknown/CrashLoopBackOff, node pressure, privileged flags, <10% free pod slots, `NoExecute` taints |
| gray | muted placeholders - `<none>`/`none`, `PreferNoSchedule` taints |
| bold | headers |

Verdict coloring per command:

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

- `pdb` also colors its `ALLOWED` count: red at 0, yellow at 1, green above.
- `svc-backends` colors `READY` red at 0 and green above, and `NOTREADY` green at
  0, yellow above.
- `rollouts` colors its `READY`/`UPDATED`/`AVAILABLE` counts against `DESIRED`:
  green at full count, red at zero, yellow in between.
- `probes` colors each probe cell by handler type (`http`/`grpc`/`tcp`/`exec`)
  green when set, a muted `-` when absent.

Control it with `--color=auto|always|never` (default `auto`, which colors only
when stdout is a terminal). `NO_COLOR` disables color; `KLENS_COLOR` sets the
default via the environment.

**Under kubecolor** (`alias kubectl=kubecolor`) klens' stdout is a pipe, so
`auto` turns color off. kubecolor passes plugin output through unchanged, so
klens' own colors survive - force them on with `--color=always` or
`export KLENS_COLOR=always`.

## Shell completion

`kubectl klens <TAB>` uses kubectl's plugin-completion mechanism (kubectl 1.26+):
kubectl looks for an executable `kubectl_complete-klens` on your `PATH` and asks
it for candidates. This repo ships that shim
(`completion/kubectl_complete-klens`), a one-liner that forwards to the plugin's
hidden `__complete` command. Load kubectl's own completion first (e.g.
`source <(kubectl completion zsh)`).

**Easiest** - let klens drop the shim for you. It writes `kubectl_complete-klens`
into krew's bin dir (already on your `PATH`), or pass `--dir` to target another
directory on your `PATH`:

```bash
kubectl klens completion install
kubectl klens completion install --dir /usr/local/bin   # non-krew install
```

**Standalone** - drop both executables on your `PATH` (from the extracted
archive):

```bash
install -m 0755 kubectl-klens /usr/local/bin/
install -m 0755 completion/kubectl_complete-klens /usr/local/bin/
```

Then `kubectl klens <TAB>` completes subcommands and flags.

## Development

```bash
make test      # go test -race ./...
make lint      # golangci-lint run
make build     # local binary
make snapshot  # goreleaser dry-run
```
