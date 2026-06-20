# kubectl-klens — `pdb` command design

Date: 2026-06-20

## Motivation

PodDisruptionBudgets (PDBs) are the single most common cause of stuck node
drains, failed cluster-autoscaler scale-downs, and blocked maintenance. Native
`kubectl get pdb` shows the raw fields:

```
NAME                            MIN AVAILABLE   MAX UNAVAILABLE   ALLOWED DISRUPTIONS   AGE
delivery-prod-entity-operator   1               N/A               1                     40d
delivery-prod-kafka             5               N/A               0                     40d
delivery-prod-kafka-exporter    1               N/A               1                     40d
```

The operator still has to reason: *"ALLOWED DISRUPTIONS = 0 — is that fine
(at floor, expected) or is it a problem (blocked drain)? Is this PDB even doing
anything, or is it orphaned with no matching pods? Is it misconfigured so it can
**never** allow a disruption?"*

`kubectl klens pdb` answers that question directly with a **VERDICT** column, so
the drain-safety state is readable at a glance instead of inferred.

## Goals

- One row per PDB with a computed drain-safety **VERDICT**.
- Compact, combined **POLICY** column (`min=5`, `min=50%`, `max=1`, `max=25%`)
  instead of native's two mostly-empty `MIN AVAILABLE` / `MAX UNAVAILABLE`
  columns.
- Surface the status fields that matter for drains: EXPECTED, DESIRED, HEALTHY,
  ALLOWED.
- Default sort by **risk descending** (worst verdicts first), like `restarts`
  and `image-count`.
- Semantic color consistent with the rest of klens (green/yellow/red/gray).

## Non-goals

- **No selector → workload resolution.** We do not list the pods each PDB
  matches, nor resolve owning Deployments/StatefulSets. That is an N+1 API
  fan-out for marginal value (YAGNI). All verdict logic is derived purely from
  the PDB `status` subresource, which the API server keeps current.
- No `--watch`, no historical disruption tracking.
- No mutation. klens is read-only.

## Scope & API

- File: `internal/view/pdb.go`, exposing `Pdb` with the standard `RunFunc`
  signature:
  `func(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error`.
- API call: `c.PolicyV1().PodDisruptionBudgets(f.NamespaceScope()).List(ctx, metav1.ListOptions{})`
  (`k8s.io/api/policy/v1`).
- Registry entry in `internal/cli/cli.go`:
  - `Name: "pdb"`, `Summary: "List PodDisruptionBudgets with a drain-safety verdict in the current namespace (-A for all)"`.
  - `CurrentNSDefault: true` — scopes to the current kubeconfig namespace absent
    `-n`/`-A`. **Must be added to the `TestCurrentNSDefaultFlags` map** in
    `cli_test.go` (the authoritative list).
  - `SortColumns: []string{"ns", "name", "policy", "expected", "desired", "healthy", "allowed", "verdict"}`.

## Columns

```
NS  NAME  POLICY  EXPECTED  DESIRED  HEALTHY  ALLOWED  VERDICT
```

| Column   | Source                                   | Notes                                                        |
|----------|------------------------------------------|--------------------------------------------------------------|
| NS       | `pdb.Namespace`                          |                                                              |
| NAME     | `pdb.Name`                               |                                                              |
| POLICY   | `pdb.Spec.MinAvailable` / `MaxUnavailable` | `min=<v>` if `MinAvailable` set, else `max=<v>` if `MaxUnavailable` set, else `none` (gray). `<v>` is the `intstr.IntOrString.String()` (so `5` or `50%`). |
| EXPECTED | `pdb.Status.ExpectedPods`                | pods the PDB currently selects. Grayed if `0`.               |
| DESIRED  | `pdb.Status.DesiredHealthy`              | minimum healthy the PDB enforces (API pre-resolves `%`).     |
| HEALTHY  | `pdb.Status.CurrentHealthy`              | currently-healthy selected pods.                             |
| ALLOWED  | `pdb.Status.DisruptionsAllowed`          | `0` red, `1` yellow, `>=2` green.                            |
| VERDICT  | computed by `pdbVerdict` (see below)     | colored by severity.                                         |

## Verdict logic

A pure helper keeps the rule testable in isolation:

```go
// pdbVerdict classifies a PDB's drain-safety state from its status fields.
// Order matters: the first matching rule wins.
func pdbVerdict(s policyv1.PodDisruptionBudgetStatus) (verdict string, sev string)
```

`sev` is one of `"ok" | "warn" | "bad" | "muted"`, mapped in the view to
`paint.OK/Warn/Bad/Muted`.

Rules, in precedence order:

| # | Verdict      | Condition                                            | Severity | Meaning                                                        |
|---|--------------|------------------------------------------------------|----------|----------------------------------------------------------------|
| 1 | `ORPHAN`     | `ExpectedPods == 0`                                  | muted    | PDB selects no pods — inert, but a sign of a stale selector.   |
| 2 | `NO-GUARD`   | `DesiredHealthy == 0 && ExpectedPods >= 2`           | bad      | Zero floor on a multi-replica workload: a drain can evict **every** replica at once — the PDB provides no protection (e.g. `minAvailable: 0`, or `maxUnavailable >= replicas`). |
| 3 | `PERMABLOCK` | `DesiredHealthy >= ExpectedPods`                     | bad      | Floor ≥ population: a disruption can **never** be allowed. Misconfigured (e.g. `minAvailable: 100%`, or `minAvailable >= replicas`). |
| 4 | `BLOCKED`    | `DisruptionsAllowed == 0 && CurrentHealthy < DesiredHealthy` | bad | Below floor *and* no disruptions allowed — a drain is stuck and pods are unhealthy. |
| 5 | `AT-FLOOR`   | `DisruptionsAllowed == 0 && CurrentHealthy >= DesiredHealthy` | warn | At (or above) the floor with nothing to spare: healthy, but a drain will block until a replacement is ready. Expected steady state for tight PDBs. |
| 6 | `OK`         | `DisruptionsAllowed >= 1`                            | ok       | At least one pod can be evicted now — drains proceed.          |

Rules 4–6 cover every remaining `DisruptionsAllowed` value once ORPHAN,
NO-GUARD, and PERMABLOCK are excluded (`< floor` → BLOCKED, `>= floor` with 0
allowed → AT-FLOOR, `>= 1` allowed → OK), so the helper always returns a verdict.

Rationale for ordering:
- ORPHAN first: with no pods, the other fields are meaningless.
- NO-GUARD before PERMABLOCK: a zero floor (`DesiredHealthy == 0`) is the
  opposite failure mode from PERMABLOCK — the PDB is toothless rather than
  permanently blocking — and is only meaningful for a multi-replica workload, so
  it is gated on `ExpectedPods >= 2` (a single replica with `minAvailable: 0` is
  a deliberate opt-out, not a risk).
- PERMABLOCK before BLOCKED: a permanent misconfiguration is a distinct, more
  actionable finding than a transient block, and its condition can overlap with
  BLOCKED's.
- AT-FLOOR (warn) vs BLOCKED (bad): both have `DisruptionsAllowed == 0`; the
  discriminator is whether current healthy has reached the desired floor.

## Sort

Default order is **risk descending** (bespoke, like `restarts`): a fixed
severity rank `bad > warn > muted > ok`, then NS, then NAME, applied via
`sort.Slice` before building the table. `t.SortBy(f.Sort)` runs after, so an
explicit `--sort` column overrides the default. Severity rank is derived from
the `sev` returned by `pdbVerdict` (no second classification).

## Coloring

- Built with `paint := kube.NewPainter(f)`; table via `kube.NewTable(out, paint, ...)`.
- VERDICT: colored by its severity.
- ALLOWED: `0` → `paint.Bad`, `1` → `paint.Warn`, `>=2` → `paint.OK`.
- EXPECTED: `paint.Muted` when `0`.
- POLICY `none` placeholder: `paint.Muted`.
- Color is off in tests (`kube.Flags{}`), so plain-output assertions stay
  byte-identical; colored behavior is covered by a dedicated `TestPdbColor`.

## Testing

`internal/view/pdb_test.go`:

1. **`TestPdbVerdict`** — table-driven over the pure helper, one case per rule
   plus boundary cases:
   - ORPHAN: `ExpectedPods=0`.
   - PERMABLOCK: `DesiredHealthy=3, ExpectedPods=3` (equal) and `>` variant.
   - BLOCKED: `DisruptionsAllowed=0, CurrentHealthy=2, DesiredHealthy=3`.
   - AT-FLOOR: `DisruptionsAllowed=0, CurrentHealthy=3, DesiredHealthy=3`.
   - OK: `DisruptionsAllowed=1` (and `>=2`).
   - Asserts both `verdict` and `sev`.
2. **`TestPdb`** — `fake.NewClientset` with one PDB per verdict; run with
   `kube.Flags{}`; assert each verdict string appears and that rows are ordered
   risk-descending (BLOCKED/PERMABLOCK before OK).
3. **`TestPdbColor`** — run with `kube.Flags{Color: true}`; assert the colored
   tokens (e.g. `\x1b[31mBLOCKED\x1b[0m`, `\x1b[32mOK\x1b[0m`, a muted `none`).

Construct fakes with explicit `Status` fields (the fake client does not run the
disruption controller, so we set `ExpectedPods`/`DesiredHealthy`/
`CurrentHealthy`/`DisruptionsAllowed` directly).

## README

Add `pdb` to the command list and the usage section, and extend the color
palette note to mention the VERDICT classifications — before committing, per
repo convention.

## Out of scope / future

- Resolving each PDB to its workload and showing replica counts (would make
  PERMABLOCK detection possible even when `ExpectedPods` is briefly stale, at
  the cost of extra API calls). Deferred unless a real need appears.
