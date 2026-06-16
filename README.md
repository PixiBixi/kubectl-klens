# kubectl-klens

A kubectl plugin for quick, read-only cluster inspection. One dispatcher,
thirteen shortcuts.

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
kubectl klens reqlim           # requests/limits per container, current ns (excl kube-system)
kubectl klens reqlim -A        # ... across all namespaces
kubectl klens images           # image occurrence counts
kubectl klens on-node <node>   # pods on a node
kubectl klens pvc              # PVCs bound to pod + node, current ns
kubectl klens pvc -A           # ... across all namespaces
kubectl klens default-sa       # pods still using the default service account
kubectl klens svc-fqdn         # in-cluster FQDN of services, current ns
kubectl klens svc-fqdn -A      # ... across all namespaces
kubectl klens autoscaler       # cluster-autoscaler status (kube-system)
kubectl klens secret <name>    # decode a secret's data, current ns
```

Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`,
`--version`.

`reqlim`, `svc-fqdn`, `secret`, and `pvc` default to the current kubeconfig
namespace (the one set by kubens/kubectx); `-A` widens to all namespaces and
`-n` targets a specific one. The other pod-scoped commands default to all
namespaces. `autoscaler` always reads from `kube-system` and ignores namespace
flags.

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
