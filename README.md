# kubectl-klens

A kubectl plugin for quick, read-only cluster inspection. One dispatcher,
sixteen shortcuts.

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
kubectl klens reqlim           # requests/limits per container, current ns (excl kube-system)
kubectl klens reqlim -A        # ... across all namespaces
kubectl klens images           # image per container per pod, current ns
kubectl klens images -A        # ... across all namespaces
kubectl klens image-count      # image occurrence counts, split registry/image/tag (cluster-wide)
kubectl klens image-count --sort registry   # sort by a column: count|registry|image|tag
kubectl klens on-node <node>   # pods on a node
kubectl klens restarts         # restarted containers + crash reason, current ns (-A for all)
kubectl klens pvc              # PVCs bound to pod + node, current ns
kubectl klens pvc -A           # ... across all namespaces
kubectl klens default-sa       # pods still using the default service account
kubectl klens svc-fqdn         # in-cluster FQDN of services, current ns
kubectl klens svc-fqdn -A      # ... across all namespaces
kubectl klens autoscaler       # cluster-autoscaler status (kube-system)
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
`--sort` is given.
`<TAB>` completes the valid column names per command.

Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`,
`--version`.

`reqlim`, `svc-fqdn`, `secret`, `pvc`, `images`, and `restarts` default to the
current kubeconfig namespace (the one set by kubens/kubectx); `-A` widens to all
namespaces and `-n` targets a specific one. The other pod-scoped commands
(including `image-count`) default to all namespaces. `autoscaler` always reads
from `kube-system` and ignores namespace flags.

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
