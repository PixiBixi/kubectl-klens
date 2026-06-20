# kubectl-klens — `pending` command design

Date: 2026-06-20

## Motivation

A pod stuck in `Pending` is one of the most common "why isn't this working?"
moments. Native `kubectl get pods` shows `Pending` but not *why*; the reason
lives in a `kubectl describe pod` event wall or in the pod's conditions. The
operator then reads a long scheduler message like:

```
0/5 nodes are available: 3 Insufficient cpu, 2 node(s) had untolerated taint {dedicated: gpu}. preemption: 0/5 nodes are available: ...
```

`kubectl klens pending` lists only the pending pods and **synthesizes the cause**
into a compact `REASON` + `DETAIL`, derived from a single `List` (no events, no
extra calls).

## Goals

- One row per `Pending` pod with a synthesized `REASON` and a compact `DETAIL`.
- Zero extra API calls: everything is read from the pod object (conditions +
  container statuses).
- Default order = oldest first (longest-stuck pods at the top).
- Semantic color consistent with the rest of klens.

## Non-goals

- No event scraping, no preemption analysis, no node-fit simulation.
- No mutation. Read-only.

## Scope & API

- File: `internal/view/pending.go`, exposing `Pending` with the standard
  `RunFunc` signature.
- API call: `c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})`.
- Keep only pods with `Status.Phase == corev1.PodPending`.
- Registry entry in `internal/cli/cli.go`: `Name: "pending"`,
  `CurrentNSDefault: true` (added to the `TestCurrentNSDefaultFlags` map),
  `SortColumns: []string{"ns", "pod", "reason"}` (AGE/DETAIL are not
  meaningfully text-sortable, so they are display-only — the guard test allows a
  subset of headers).

## Columns

```
NS  POD  AGE  REASON  DETAIL
```

| Column | Source                                             |
|--------|----------------------------------------------------|
| NS     | `pod.Namespace`                                    |
| POD    | `pod.Name`                                         |
| AGE    | `duration.ShortHumanDuration(time.Since(pod.CreationTimestamp.Time))` |
| REASON | synthesized (see below)                            |
| DETAIL | synthesized compact cause, or muted `-`            |

## REASON / DETAIL derivation (precedence)

1. **Unschedulable** — if the `PodScheduled` condition is `False`:
   - `REASON` = condition `Reason` (typically `Unschedulable`).
   - `DETAIL` = parsed dominant cause of condition `Message` (see parser).
2. **Stuck pulling/creating** — else, the first container in `Waiting` state
   (scan `Status.ContainerStatuses` then `Status.InitContainerStatuses`):
   - `REASON` = `waiting.Reason` (`ImagePullBackOff`, `ErrImagePull`,
     `CreateContainerConfigError`, `InvalidImageName`, `ContainerCreating`,
     `PodInitializing`, ...).
   - `DETAIL` = for image-related reasons (`ImagePullBackOff`, `ErrImagePull`,
     `InvalidImageName`), the offending container `Image`; otherwise muted `-`.
3. **Fallback** — `REASON` = `Pending`, `DETAIL` = muted `-`.

## Scheduler-message parser

`schedulerCause(msg string) string` turns the verbose scheduler message into one
compact clause, with a graceful fallback so it is never empty:

1. Take the substring after the first `available: ` (drop the
   `0/N nodes are available` prefix). If absent, go to step 5.
2. Cut at the first `. ` to drop the trailing `preemption: ...` sentence.
3. Split the remainder on `, ` into clauses like `3 Insufficient cpu`,
   `2 node(s) had untolerated taint {dedicated: gpu}`.
4. For each clause, strip a leading `<int> ` count; keep the clause with the
   **largest** count (the dominant blocker). Trim any trailing ` {...}` blob and
   render `"<phrase> (<count> nodes)"`, e.g. `Insufficient cpu (3 nodes)`.
5. Fallback: return `msg` trimmed to its first sentence, capped at ~60 runes.

The parser is intentionally tolerant: any unrecognized shape falls through to the
trimmed raw message, so `DETAIL` always carries something actionable.

## Color

- Built with `paint := kube.NewPainter(f)`.
- `REASON` is colored via the shared `paint.Status` classifier. **This change
  extends `paint.Status`** (single source of truth in `internal/kube/color.go`):
  - add to the bad/red set: `Unschedulable`, `ImagePullBackOff`, `ErrImagePull`,
    `CreateContainerConfigError`, `InvalidImageName`.
  - add to the warn/yellow set: `PodInitializing` (`ContainerCreating` and
    `Pending` are already warn-classified).
  - Beneficial ripple: `on-node` and `restarts` will now also color these
    image/scheduling states consistently. Existing tests assert their own
    tokens, so this only adds color where there was none.
- `DETAIL` `-` placeholder uses `paint.Muted`.

## Sort

Default = oldest first: `sort.SliceStable` by `CreationTimestamp` ascending
before building the table (bespoke, like `restarts`). `t.SortBy(f.Sort)` then
applies any explicit `--sort` (ns|pod|reason), overriding the default.

## Testing

`internal/view/pending_test.go`:

1. **`TestSchedulerCause`** — table-driven over the pure parser:
   - dominant-count pick: `0/5 nodes are available: 3 Insufficient cpu, 2 node(s) had untolerated taint {x: y}.` → `Insufficient cpu (3 nodes)`.
   - single clause, no preemption tail.
   - taint clause with `{...}` blob stripped.
   - unparseable message → trimmed raw fallback.
2. **`TestPending`** — `fake.NewClientset` with: an Unschedulable pod (set
   `Status.Conditions` `PodScheduled=False`, `Message=...`), an
   `ImagePullBackOff` pod (container status waiting + spec image), a `Running`
   pod (must be excluded). Assert the unschedulable cause, the bad image string,
   and that the running pod is absent. Run with `kube.Flags{}` (color off).
3. **`TestPendingColor`** — `kube.Flags{Color: true}`; assert
   `\x1b[31mUnschedulable\x1b[0m` and `\x1b[31mImagePullBackOff\x1b[0m`.

The fake client does not run the scheduler, so set pod `Status` explicitly.

## README

Add `pending` to the command list and the current-namespace-default list; the
color note already covers red/yellow status tokens.
