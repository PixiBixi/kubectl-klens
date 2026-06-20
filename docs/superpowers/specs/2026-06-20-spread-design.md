# kubectl-klens — `spread` command design

Date: 2026-06-20

## Motivation

A workload can run "3 replicas" and still be a single point of failure if all
three landed on the same node (or the same zone). Native tooling makes you
cross-reference `kubectl get pods -o wide` against node topology by hand.
`kubectl klens spread` does that join: it groups a namespace's replicas by their
owning workload, counts the distinct nodes and zones they occupy, and emits a
placement **VERDICT** (e.g. `SPOF-NODE`, `SPOF-ZONE`, `SPREAD`).

This complements `pdb` (drain-safety) with the *placement* side of availability.

## Goals

- One row per workload with replica count, distinct node count, distinct zone
  count, and a placement verdict.
- Cheap: two `List` calls total (pods + nodes), no per-pod API fan-out.
- Default sort by risk descending.

## Non-goals

- No owner-chain API resolution. ReplicaSet → Deployment is collapsed by a name
  heuristic (strip the trailing pod-template-hash segment), not by fetching the
  ReplicaSet's owner.
- DaemonSet, Job/CronJob, and standalone (uncontrolled) pods are out of scope:
  DaemonSets span nodes by design, batch pods aren't HA replicas.
- No mutation.

## Scope & API

- File: `internal/view/spread.go`, exposing `Spread` (standard `RunFunc`) + pure
  helper `spreadVerdict(replicas, nodes, zones int)`.
- API: `c.CoreV1().Pods(f.NamespaceScope()).List(...)` and
  `c.CoreV1().Nodes().List(...)` (for the node → zone map via the
  `topology.kubernetes.io/zone` label, same key `zones` uses).
- Registry entry: `Name: "spread"`, `CurrentNSDefault: true` (added to the
  `TestCurrentNSDefaultFlags` map),
  `SortColumns: []string{"ns", "workload", "replicas", "nodes", "zones", "verdict"}`.

## Grouping

For each pod with a non-empty `Spec.NodeName` (scheduled):

1. `ref := metav1.GetControllerOf(&pod)`; skip if nil (uncontrolled).
2. Workload key by owner kind:
   - `ReplicaSet` → `Deployment/<trimHash(name)>` (strip the last `-<segment>`).
   - `StatefulSet`, `ReplicationController` → `<Kind>/<name>`.
   - anything else (DaemonSet, Job, ...) → skip.
3. Accumulate per `namespace + "/" + workload`: replica count, set of node names,
   set of non-empty zones.

`trimHash` strips the final `-<segment>` of a ReplicaSet name (the
pod-template-hash) to recover the Deployment name; bare ReplicaSets (rare) may be
mislabeled `Deployment/...`, an accepted cosmetic edge.

## Columns

```
NS  WORKLOAD  REPLICAS  NODES  ZONES  VERDICT
```

REPLICAS = scheduled pod count; NODES = distinct nodes; ZONES = distinct zones
(0 when no node carries a zone label); VERDICT colored by severity.

## Verdict logic

```go
func spreadVerdict(replicas, nodes, zones int) (verdict, sev string)
```

| # | Verdict      | Condition                          | Severity | Meaning                                          |
|---|--------------|------------------------------------|----------|--------------------------------------------------|
| 1 | `SINGLE`     | `replicas <= 1`                    | muted    | One replica — non-HA by design, informational.   |
| 2 | `SPOF-NODE`  | `nodes <= 1` (and `replicas >= 2`) | bad      | All replicas on one node: node loss = full outage.|
| 3 | `SPREAD`     | `zones >= 2`                       | ok       | Replicas across multiple zones.                  |
| 4 | `SPOF-ZONE`  | `zones == 1`                       | warn     | Multiple nodes but a single zone.                |
| 5 | `MULTI-NODE` | `zones == 0`                       | muted    | Multiple nodes, zone topology unknown (unlabeled).|

First match wins; rules are total. `sev` maps via the shared `sevRank`/`sevPaint`
helpers (from `pdb`).

## Sort

Default = risk descending: `sort.SliceStable` by `sevRank(sev)`, then NS, then
WORKLOAD (bespoke, like `pdb`). Iteration order of the group map is captured in
insertion order first to keep output deterministic before the sort.
`t.SortBy(f.Sort)` applies any explicit `--sort`.

## Coloring

`paint := kube.NewPainter(f)`. VERDICT colored by severity (green for `SPREAD`,
red for `SPOF-NODE`, etc.). Numeric cells plain — the verdict already carries the
signal.

## Testing

`internal/view/spread_test.go`:

1. **`TestSpreadVerdict`** — table-driven: single (`1,1,0`), spof-node (`3,1,1`),
   spof-zone (`3,2,1`), spread (`2,2,2`), multi-node (`2,2,0`). Asserts verdict
   and sev.
2. **`TestSpread`** — `fake.NewClientset` with three nodes (zones `a`,`b`,`a`),
   a Deployment (`web` via ReplicaSet `web-abc123`, 2 pods on different zones →
   `SPREAD`), a StatefulSet (`db`, 2 pods on the same node → `SPOF-NODE`), and a
   DaemonSet pod (must be excluded). Assert verdicts, `Deployment/web` label,
   DaemonSet absence, and risk-descending order (`db` before `web`).
3. **`TestSpreadColor`** — `kube.Flags{Color: true}`; assert
   `\x1b[31mSPOF-NODE\x1b[0m` and `\x1b[32mSPREAD\x1b[0m`.

## README

Add `spread` to the command list, the current-namespace-default list, and note
its VERDICT colors in the color section.
