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
| `pods-per-node` | pod count per node |
| `max-pods` | pod ceiling, non-terminated count, free slots per node |
| `node-conditions` | node readiness + memory/disk/pid pressure |
| `on-node <node>` | pods on a node |

### Workloads & resources

| Command | Shows |
| --- | --- |
| `reqlim` † | requests/limits per container (excl. kube-system) |
| `no-limits` † | containers missing CPU/mem limits |
| `no-requests` † | containers missing CPU/mem requests |
| `images` † | image per container per pod |
| `image-count` | image counts, split registry/image/tag (cluster-wide) |
| `restarts` † | restarted containers + crash reason + last exit code |

### Storage & networking

| Command | Shows |
| --- | --- |
| `pvc` † | PVCs bound to pod + node |
| `svc-fqdn` † | in-cluster FQDN of services |

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
| `probes` † | readiness/liveness/startup probes + verdict (excl kube-system) |

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
`--version`.

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
recent scaling event is easy to spot — it is only populated from the
structured-YAML format.

## Sorting

Most table commands accept `--sort <column>` to order rows by one of their
columns (e.g. `kubectl klens zones --sort region`, `kubectl klens nodes --sort
nodepool`). Sorting is ascending, with numeric columns ordered by value.
`<TAB>` completes the valid column names per command.

Defaults that differ from ascending:

- `image-count` and `restarts` — count-descending.
- `autoscaler` — `LAST-CHANGE` descending (most recently changed nodegroup
  first). Sortable columns: `nodegroup|health|ready|target|min|max|scaleup|scaledown|last-change`.
- Verdict commands (`pdb`, `hpa`, `spread`, `probes`) — `VERDICT` by severity
  (least-risky first), so the riskiest rows land at the bottom, nearest the
  prompt.

Pass `--sort <column>` to override any of these. `image-count` sortable columns:
`count|registry|image|tag`.

## Color

klens colorizes its tables:

| Color | Meaning |
| --- | --- |
| green | good — Ready/Healthy/Bound/Running, roomy free pod slots |
| yellow | warning — Pending, high restart counts, floating `latest` tags, <25% free pod slots, `NoSchedule` taints |
| red | bad — NotReady/Unknown/CrashLoopBackOff, node pressure, privileged flags, <10% free pod slots, `NoExecute` taints |
| gray | muted placeholders — `<none>`/`none`, `PreferNoSchedule` taints |
| bold | headers |

Verdict coloring per command:

| Command | green | yellow | red | gray |
| --- | --- | --- | --- | --- |
| `pdb` | `OK` | `AT-FLOOR` | `BLOCKED`/`PERMABLOCK`/`NO-GUARD` | `ORPHAN` |
| `hpa` | `OK` | `SCALING` | `MAXED`/`NO-METRICS` | `AT-MIN` |
| `spread` | `SPREAD` | `SPOF-ZONE` | `SPOF-NODE` | `SINGLE`/`MULTI-NODE` |
| `probes` | `OK` | `NO-LIVENESS` | `NO-READINESS`/`NO-PROBES` | — |

- `pdb` also colors its `ALLOWED` count: red at 0, yellow at 1, green above.
- `probes` colors each probe cell by handler type (`http`/`grpc`/`tcp`/`exec`)
  green when set, a muted `-` when absent.

Control it with `--color=auto|always|never` (default `auto`, which colors only
when stdout is a terminal). `NO_COLOR` disables color; `KLENS_COLOR` sets the
default via the environment.

**Under kubecolor** (`alias kubectl=kubecolor`) klens' stdout is a pipe, so
`auto` turns color off. kubecolor passes plugin output through unchanged, so
klens' own colors survive — force them on with `--color=always` or
`export KLENS_COLOR=always`.

## Shell completion

`kubectl klens <TAB>` uses kubectl's plugin-completion mechanism (kubectl 1.26+):
kubectl looks for an executable `kubectl_complete-klens` on your `PATH` and asks
it for candidates. This repo ships that shim
(`completion/kubectl_complete-klens`), a one-liner that forwards to the plugin's
hidden `__complete` command. Load kubectl's own completion first (e.g.
`source <(kubectl completion zsh)`).

**Easiest** — let klens drop the shim for you. It writes `kubectl_complete-klens`
into krew's bin dir (already on your `PATH`), or pass `--dir` to target another
directory on your `PATH`:

```bash
kubectl klens completion install
kubectl klens completion install --dir /usr/local/bin   # non-krew install
```

**Standalone** — drop both executables on your `PATH` (from the extracted
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
