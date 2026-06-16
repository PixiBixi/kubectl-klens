# kubectl-klens

A kubectl plugin for quick, read-only cluster inspection. One dispatcher,
nine shortcuts.

## Install (krew, personal)

```bash
kubectl krew install --manifest=.krew.yaml --archive=dist/kubectl-klens_<ver>_<os>_<arch>.tar.gz
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

## Development

```bash
make test      # go test -race ./...
make lint      # go vet + staticcheck
make build     # local binary
make snapshot  # goreleaser dry-run
```
