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
kubectl klens reqlim           # requests/limits per container (excl kube-system)
kubectl klens images           # image occurrence counts
kubectl klens on-node <node>   # pods on a node
kubectl klens pvc              # PVCs bound to pod + node
```

Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`,
`--version`.

## Development

```bash
make test      # go test -race ./...
make lint      # go vet + staticcheck
make build     # local binary
make snapshot  # goreleaser dry-run
```
