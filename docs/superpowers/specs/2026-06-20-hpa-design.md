# kubectl-klens — `hpa` command design

Date: 2026-06-20

## Motivation

`kubectl get hpa` shows current/target metrics and replica counts but leaves the
operator to infer the state that matters: *is this HPA pinned at its ceiling
(under-provisioned, no headroom to absorb more load), is it blind because it
can't read metrics, or is it scaling right now?* `kubectl klens hpa` synthesizes
that into a **VERDICT**, mirroring `pdb`'s drain-safety verdict for autoscaling.

## Goals

- One row per HPA with a computed autoscaling **VERDICT**.
- Surface the scale target and the replica envelope (MIN/MAX/CURRENT/DESIRED).
- Default sort by risk descending (worst verdicts first), like `pdb`.
- Color consistent with the rest of klens.

## Non-goals

- No per-metric `current/target` parsing (the verbose, brittle part of
  `autoscaling/v2`). The verdict + replica envelope is the value; the metric
  breakdown is left to `kubectl describe hpa`.
- No mutation.

## Scope & API

- File: `internal/view/hpa.go`, exposing `Hpa` (standard `RunFunc`) + a pure
  helper `hpaVerdict(spec, status)`.
- API: `c.AutoscalingV2().HorizontalPodAutoscalers(f.NamespaceScope()).List(ctx, metav1.ListOptions{})`
  (`k8s.io/api/autoscaling/v2`).
- Registry entry: `Name: "hpa"`, `CurrentNSDefault: true` (added to the
  `TestCurrentNSDefaultFlags` map),
  `SortColumns: []string{"ns", "name", "ref", "min", "max", "current", "desired", "verdict"}`.

## Columns

```
NS  NAME  REF  MIN  MAX  CURRENT  DESIRED  VERDICT
```

| Column  | Source                                                        |
|---------|---------------------------------------------------------------|
| NS      | `hpa.Namespace`                                               |
| NAME    | `hpa.Name`                                                    |
| REF     | `Spec.ScaleTargetRef.Kind + "/" + Name` (e.g. `Deployment/api`) |
| MIN     | `*Spec.MinReplicas` (defaults to `1` when nil)                |
| MAX     | `Spec.MaxReplicas`                                            |
| CURRENT | `Status.CurrentReplicas` — colored `Bad` when `>= MAX`        |
| DESIRED | `Status.DesiredReplicas`                                      |
| VERDICT | `hpaVerdict` (colored by severity)                           |

## Verdict logic

```go
func hpaVerdict(spec autoscalingv2.HorizontalPodAutoscalerSpec, st autoscalingv2.HorizontalPodAutoscalerStatus) (verdict, sev string)
```

Rules in precedence order (first match wins, so the helper is total):

| # | Verdict      | Condition                                                | Severity | Meaning                                              |
|---|--------------|----------------------------------------------------------|----------|------------------------------------------------------|
| 1 | `NO-METRICS` | `ScalingActive` condition present and `False`            | bad      | HPA can't read metrics — it's flying blind.          |
| 2 | `MAXED`      | `CurrentReplicas >= MaxReplicas`                         | bad      | Pinned at the ceiling: no headroom to scale up.      |
| 3 | `SCALING`    | `CurrentReplicas != DesiredReplicas`                    | warn     | Actively converging toward desired.                  |
| 4 | `AT-MIN`     | `CurrentReplicas <= effective MinReplicas`              | muted    | Idle at the floor (low load) — informational.        |
| 5 | `OK`         | otherwise                                                | ok       | Healthy mid-range.                                   |

`sev` is one of `ok|warn|bad|muted`, mapped to `paint.OK/Warn/Bad/Muted` (reuse
the `sevRank`/`sevPaint` helpers introduced for `pdb`).

`ScalingActive` is read via a small helper that returns true only when the
condition exists **and** its status is `corev1.ConditionFalse`.

## Sort

Default = risk descending: `sort.SliceStable` by `sevRank(sev)` then NS then
NAME (bespoke, like `pdb`); `t.SortBy(f.Sort)` applies any explicit `--sort`.

## Coloring

`paint := kube.NewPainter(f)`. VERDICT colored by severity; CURRENT colored
`Bad` when `>= MAX` (visually reinforces MAXED). Other cells plain.

## Testing

`internal/view/hpa_test.go`:

1. **`TestHpaVerdict`** — table-driven, one case per rule plus boundaries:
   no-metrics (ScalingActive False), maxed (`current == max`), scaling
   (`current != desired`), at-min (nil min defaults to 1; explicit min), ok.
   Asserts `verdict` and `sev`.
2. **`TestHpa`** — `fake.NewClientset` with a MAXED and an OK HPA; assert
   verdicts, the `Deployment/<name>` REF, and risk-descending order.
3. **`TestHpaColor`** — `kube.Flags{Color: true}`; assert
   `\x1b[31mMAXED\x1b[0m` and `\x1b[32mOK\x1b[0m`.

Set `Status` fields explicitly (the fake client runs no HPA controller).

## README

Add `hpa` to the command list, the current-namespace-default list, and note its
VERDICT colors in the color section.
