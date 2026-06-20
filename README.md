# kubectl-klens

A kubectl plugin for quick, read-only cluster inspection. One dispatcher,
twenty shortcuts.

## Install (krew, personal index)

Each tagged release publishes `plugins/klens.yaml` to this repo, so the repo
itself is a krew custom index. On any machine:

```bash
kubectl krew index add pixibixi https://github.com/PixiBixi/kubectl-klens.git
kubectl krew install pixibixi/klens
kubectl krew upgrade pixibixi/klens   # later, to update
```

Or download a release archive, extract `kubectl-klens` onto your `PATH`, and
invoke it as `kubectl klens`.

## Usage

```bash
kubectl klens nodes            # nodes + GKE nodepool + instance-type
kubectl klens taints           # taints per node
kubectl klens capacity         # CPU/mem capacity + allocatable
kubectl klens zones            # region/zone per node
kubectl klens pods-per-node    # pod count per node
kubectl klens max-pods         # pod ceiling (allocatable), current count, free slots per node
kubectl klens node-conditions  # node readiness + memory/disk/pid pressure
kubectl klens reqlim           # requests/limits per container, current ns (excl kube-system)
kubectl klens reqlim -A        # ... across all namespaces
kubectl klens no-limits        # containers missing CPU/mem limits, current ns (-A for all)
kubectl klens no-requests      # containers missing CPU/mem requests, current ns (-A for all)
kubectl klens images           # image per container per pod, current ns
kubectl klens images -A        # ... across all namespaces
kubectl klens image-count      # image occurrence counts, split registry/image/tag (cluster-wide)
kubectl klens image-count --sort registry   # sort by a column: count|registry|image|tag
kubectl klens on-node <node>   # pods on a node
kubectl klens restarts         # restarted containers + crash reason, current ns (-A for all)
kubectl klens pvc              # PVCs bound to pod + node, current ns
kubectl klens pvc -A           # ... across all namespaces
kubectl klens default-sa       # pods still using the default service account
kubectl klens privileged       # containers with privileged/host security flags, current ns (-A for all)
kubectl klens svc-fqdn         # in-cluster FQDN of services, current ns
kubectl klens svc-fqdn -A      # ... across all namespaces
kubectl klens pdb              # PodDisruptionBudgets + drain-safety verdict, current ns (-A for all)
kubectl klens pending          # Pending pods + synthesized blocking reason, current ns (-A for all)
kubectl klens hpa              # HorizontalPodAutoscalers + autoscaling verdict, current ns (-A for all)
kubectl klens autoscaler       # cluster-autoscaler: cluster-wide summary + per-nodegroup table (kube-system)
kubectl klens autoscaler --sort target   # sort the nodegroup table by a column: nodegroup|health|ready|target|min|max|scaleup|scaledown|last-change
kubectl klens secret           # pick a secret, then a key (interactive)
kubectl klens secret <name>    # pick a key of <name> (interactive)
kubectl klens secret <name> <key>  # decode and print one key's value
kubectl klens secret <name> all    # decode and print all keys
```

`secret` opens interactive pickers when run in a terminal; when piped (script,
CI) it falls back to plain listings (`secret` lists secrets, `secret <name>`
lists keys). In a picker, press `/` to filter the list as you type. A secret
with a single key skips the key picker and decodes that key directly.

Commands accept their singular or plural form interchangeably (`kubectl klens
image` ≡ `kubectl klens images`, `node` ≡ `nodes`, ...).

Most table commands accept `--sort <column>` to order rows by one of their
columns (e.g. `kubectl klens zones --sort region`, `kubectl klens nodes --sort
nodepool`). Sorting is ascending, with numeric columns ordered by value;
`image-count` and `restarts` keep their count-descending default unless
`--sort` is given, and `autoscaler` defaults to `LAST-CHANGE` descending (most
recently changed nodegroup first) unless `--sort` is given.
`<TAB>` completes the valid column names per command.

Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`,
`--color`, `--version`.

`reqlim`, `svc-fqdn`, `secret`, `pvc`, `images`, `restarts`, `no-limits`,
`no-requests`, `privileged`, `pdb`, `pending`, and `hpa` default to the current kubeconfig namespace (the one set by kubens/kubectx); `-A` widens to all
namespaces and `-n` targets a specific one. The other pod-scoped commands
(including `image-count`) default to all namespaces. `autoscaler` always reads
from `kube-system` and ignores namespace flags; it renders the
cluster-autoscaler status (both the structured-YAML format from CA 1.30+ and the
older legacy text) into a cluster-wide summary plus a per-nodegroup table,
falling back to the raw status when neither format is recognized. The table's
`LAST-CHANGE` column shows each nodegroup's most recent `lastTransitionTime`
(across its health/scale-up/scale-down conditions), so a recent scaling event is
easy to spot; it is only populated from the structured-YAML format.

## Color

klens colorizes its tables: green = good (Ready/Healthy/Bound/Running, roomy
free pod slots), yellow = warning (Pending, high restart counts, floating
`latest` tags, under 25% free pod slots, `NoSchedule` taints), red = bad
(NotReady/Unknown/CrashLoopBackOff, node pressure, privileged flags, under 10%
free pod slots, `NoExecute` taints), gray = muted placeholders (`<none>`/`none`,
`PreferNoSchedule` taints), bold = headers.

`pdb` colors its `VERDICT` by drain-safety: `OK` green, `AT-FLOOR` yellow,
`BLOCKED`/`PERMABLOCK`/`NO-GUARD` red, `ORPHAN` gray; the `ALLOWED` count is red
at 0, yellow at 1, green above. `hpa` colors its `VERDICT` likewise: `OK` green,
`SCALING` yellow, `MAXED`/`NO-METRICS` red, `AT-MIN` gray.

Control it with `--color=auto|always|never` (default `auto`, which colors only
when stdout is a terminal). `NO_COLOR` disables color; `KLENS_COLOR` sets the
default via the environment.

Under kubecolor (`alias kubectl=kubecolor`) klens' stdout is a pipe, so `auto`
turns color off. kubecolor passes plugin output through unchanged, so klens'
own colors survive — force them on with `--color=always` or, once in your
shell, `export KLENS_COLOR=always`.

## Shell completion

`kubectl klens <TAB>` completion uses kubectl's plugin-completion mechanism
(kubectl 1.26+): kubectl looks for an executable `kubectl_complete-klens` on
your `PATH` and asks it for candidates. This repo ships that shim
(`completion/kubectl_complete-klens`), a one-liner that forwards to the plugin's
hidden `__complete` command. Load kubectl's own completion first (e.g.
`source <(kubectl completion zsh)`).

Easiest — let klens drop the shim for you. It writes
`kubectl_complete-klens` into krew's bin dir (already on your `PATH`), or
pass `--dir` to target another directory on your `PATH`:

```bash
kubectl klens completion install
kubectl klens completion install --dir /usr/local/bin   # non-krew install
```

Standalone — drop both executables on your `PATH` (from the extracted archive):

```bash
install -m 0755 kubectl-klens /usr/local/bin/
install -m 0755 completion/kubectl_complete-klens /usr/local/bin/
```

Then `kubectl klens <TAB>` completes subcommands and flags.

## Development

```bash
make test      # go test -race ./...
make lint      # go vet + staticcheck
make build     # local binary
make snapshot  # goreleaser dry-run
```
