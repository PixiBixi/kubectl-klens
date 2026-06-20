# kubectl-klens — `probes` command design

Date: 2026-06-20

## Motivation

A container with no readiness probe receives traffic the instant its process
starts — before the app can serve — so a rolling deploy quietly emits 5xx until
every replica is up. A container with no liveness probe never self-heals from a
hang. Native tooling makes you open each pod spec to see this. `kubectl klens
probes` lists every container's readiness/liveness/startup state and emits a
**VERDICT**, so the gap that looks healthy (`NO-READINESS`) is as visible as the
one that looks broken.

This sits in the `no-limits`/`no-requests` family (per-container config hygiene)
but adds a synthesized verdict like `pdb`/`hpa`.

## Goals

- One row per regular container with its readiness, liveness, and startup probe
  handler types and a reliability verdict.
- Cheap: one `List` call (pods), no per-pod API fan-out.
- Default sort by risk descending.

## Non-goals

- No probe-quality heuristics (aggressive `failureThreshold`, liveness ==
  readiness, low `initialDelaySeconds`). v1 reports presence/absence only.
- No Service cross-reference: the verdict can't know whether a pod is actually
  behind a Service, so `NO-READINESS` is reported for every eligible container.
  Job/CronJob pods (which don't want readiness) are excluded to cut the noise;
  the residual edge (a Service-less Deployment) is accepted.
- Init containers (including native sidecars with probes) are out of scope; only
  `Spec.Containers` are inspected.
- No mutation.

## Scope & API

- File: `internal/view/probes.go`, exposing `Probes` (standard `RunFunc`) + pure
  helper `probesVerdict(hasReadiness, hasLiveness bool)`.
- API: `c.CoreV1().Pods(f.NamespaceScope()).List(...)` only.
- Registry entry: `Name: "probes"`, `CurrentNSDefault: true` (added to the
  `TestCurrentNSDefaultFlags` map),
  `SortColumns: []string{"ns", "pod", "container", "readiness", "liveness", "startup", "verdict"}`.
- Excludes `kube-system` with the same `if p.Namespace == "kube-system" { continue }`
  guard used by `reqlim`/`no-limits`/`no-requests` (platform-managed pods are
  noise the user can't act on).

## Pod eligibility

For each pod:

1. Skip `kube-system` (as above).
2. `ref := metav1.GetControllerOf(&pod)`; skip if `ref.Kind == "Job"` — this also
   covers CronJobs, whose pods are owned by the Job they spawn. Batch pods are
   not long-running servers and don't want readiness/liveness.
3. All other pods are eligible: Deployment/ReplicaSet, StatefulSet, DaemonSet,
   ReplicationController, and standalone (uncontrolled) pods.

Then one row per `pod.Spec.Containers` entry.

## Columns

```
NS  POD  CONTAINER  READINESS  LIVENESS  STARTUP  VERDICT
```

READINESS/LIVENESS/STARTUP show the probe **handler type** — `http`, `tcp`,
`grpc`, or `exec` — when the probe is set, or a muted `-` when absent. The
handler type is free (same field access) and more useful than a checkmark (e.g.
spotting heavy `exec` probes). STARTUP is informational only; it does not affect
the verdict.

## Verdict logic

```go
func probesVerdict(hasReadiness, hasLiveness bool) (verdict, sev string)
```

| # | Verdict        | Condition                          | Severity | Meaning                                                        |
|---|----------------|------------------------------------|----------|----------------------------------------------------------------|
| 1 | `NO-PROBES`    | `!hasReadiness && !hasLiveness`    | bad      | No traffic gating and no self-healing: container is unmonitored.|
| 2 | `NO-READINESS` | `!hasReadiness` (liveness present) | bad      | Traffic is routed before/while the app is unready — invisible 5xx during rollouts. |
| 3 | `NO-LIVENESS`  | `!hasLiveness` (readiness present) | warn     | A hung container won't be restarted automatically.            |
| 4 | `OK`           | both present                       | ok       | Readiness gates traffic, liveness restarts on hang.           |

First match wins; rules are total. `NO-READINESS` is `bad` (not `warn`) because a
missing readiness probe is the classic "looks healthy, silently serves errors"
failure — the same reasoning as `pdb`'s `NO-GUARD`. `sev` maps via the shared
`sevRank`/`sevPaint` helpers (from `pdb`).

## Sort

Default = risk descending: `sort.SliceStable` by `sevRank(sev)`, then NS, POD,
CONTAINER (bespoke, like `pdb`/`hpa`/`spread`). `t.SortBy(f.Sort)` applies any
explicit `--sort`.

## Coloring

`paint := kube.NewPainter(f)`. VERDICT colored by severity (red `NO-PROBES`/
`NO-READINESS`, yellow `NO-LIVENESS`, green `OK`). Probe cells: present handler
type green (`paint.OK`), absent `-` muted (`paint.Muted`) — surfaces the healthy
state too, per the established color preference, while the VERDICT carries the
aggregate signal.

## Testing

`internal/view/probes_test.go`:

1. **`TestProbesVerdict`** — table-driven over the pure helper: `(false,false)` →
   `NO-PROBES`/bad, `(false,true)` → `NO-READINESS`/bad, `(true,false)` →
   `NO-LIVENESS`/warn, `(true,true)` → `OK`/ok.
2. **`TestProbes`** — `fake.NewClientset` with: a Deployment pod with both probes
   (→ `OK`, handlers `http`/`http`), a pod missing readiness (→ `NO-READINESS`),
   a pod with neither (→ `NO-PROBES`), a Job-owned pod (excluded), a kube-system
   pod (excluded). Assert handler types, verdicts, both exclusions, and
   risk-descending order (`NO-PROBES`/`NO-READINESS` before `OK`).
3. **`TestProbesColor`** — `kube.Flags{Color: true}`; assert
   `\x1b[31mNO-READINESS\x1b[0m`, `\x1b[33mNO-LIVENESS\x1b[0m`,
   `\x1b[32mOK\x1b[0m`, and a green handler token (e.g. `\x1b[32mhttp\x1b[0m`).

## README

Add `probes` to the command list, the current-namespace-default list, and note
its VERDICT colors in the color section.
