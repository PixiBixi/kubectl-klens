# Performance

What is actually slow in klens, how it was measured, and which optimisations were
tried and rejected. Read this before "optimising" anything here - most of the
obvious candidates were measured and do nothing.

## Where the time goes

klens is a one-shot CLI dominated by **the apiserver**, not by Go. Local
processing of a 6500-pod cluster is tens of milliseconds; the command takes
seconds. The order of magnitude that matters:

| Cost | Scale on a 6500-pod cluster |
| --- | --- |
| Listing every pod | ~86 MiB, ~3s (measured, see `internal/kube/client.go`) |
| Local processing of that list | ~20-35 ms |
| Rendering the table | ~10 ms |

So the lever is **not fetching what you do not need**. Allocation tuning in the
view layer is noise by comparison.

## Benchmarks

```bash
make bench                                    # everything, 1 run
make bench BENCH=PvcResize COUNT=6            # one view, 6 runs for benchstat
make bench BENCH=TableFlush COUNT=6 > new.txt
benchstat old.txt new.txt
```

`internal/kube` holds micro-benchmarks (`visibleWidth`, `Table.Flush`);
`internal/view/bench_test.go` runs whole `RunFunc`s end to end.

**What they cannot tell you:** the fake clientset serves from memory, so there is
no apiserver latency and no protobuf decode - the two things that dominate a real
run - and it DeepCopies every object it hands out, which a real client does not.
Treat the numbers as a *local processing* ratchet, never as a wall-clock
prediction. Always compare with `benchstat` over `COUNT=6`; single runs on a
laptop swing 10%.

## The pattern that pays: lazy and narrowed lists

Two shapes, both worth looking for in a new view:

**Defer a list until you know you need it.** `pvc-resize` needs the pod list only
to fill its `POD` column, and its usual output is an empty table. It now lists
claims first and returns early when there is nothing to report - so the ~86 MiB
pod list is never issued on the common path. `ingress` does the same with
Services and Secrets: with no ingress in scope, neither is fetched.

**Narrow a list to where the rows are.** Once `pvc-resize` does have rows, they
sit in one or two namespaces, so it lists pods per namespace rather than
cluster-wide, up to `maxNamespaceFanout` (8) before falling back to a single
list.

Measured (fake clientset, so the real gain is larger):

| Benchmark | sec/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `PvcResizeSettled` (nothing resizing) | -64.7% | -67.6% | -68.7% |
| `PvcResizeFewRows` (3 claims resizing) | -59.1% | -65.5% | -66.9% |
| `IngressNone` (no ingress, 6500 secrets) | -99.9% | -99.98% | -99.9% |

The trade: a deferred list no longer overlaps with the one before it. That is
paid only on the path that has rows, which narrowing then more than refunds.
`BenchmarkPvcResize` (all 6500 claims resizing, past the fanout cap) is the one
case that is slower than before, ~52ms against ~34ms, and it is not a real
cluster state.

**Narrow with `-n <glob>`.** The same lever, handed to the user: `-n 'be-*'`
expands to the matched namespaces and issues one List each instead of a
cluster-wide one. Measured on the bench shape (6500 pods over 40 namespaces),
against 11.4ms / 42.9MiB for the cluster-wide list:

| Scope | sec/op | B/op |
| --- | --- | --- |
| fan-out, 16 of 40 namespaces | 6.4 ms | 20.4 MiB |
| fan-out, 32 of 40 | 12.9 ms | 40.8 MiB |
| fan-out, 40 of 40 | 16.5 ms | 51.0 MiB |

So the fan-out wins while it covers well under half the cluster and loses
once it covers most of it, which is what sets `kube.MaxNamespaceFanout` at
16. The fake clientset understates the win twice over: a real targeted List
also skips the bytes and the protobuf decode of every namespace it did not
match, and paging makes the cluster-wide List *sequential* (4 requests for
6500 pods) where the fan-out's are concurrent.

**Ask the controllers, not the pods (`--by-owner`).** `reqlim`, `no-limits`,
`no-requests`, `images`, `probes` and `qos` all read a pod's container spec
and nothing else - `qos` is the one exception, and it already has a
from-spec fallback for when a pod's `status.qosClass` is unset. That is
exactly what makes `--by-owner` a source switch rather than a client-side
reshape: with the flag, `podsForView` lists the four controller kinds
instead of every pod and hands each view a synthetic pod built from the
controller's template, which then flows through the same per-container loop
unmodified.

The controllers are two orders of magnitude fewer objects than the pods
behind them. Measured on a 7209-pod GKE cluster, `qos -A`:

| Listing | objects | payload | wall clock |
| --- | --- | --- | --- |
| pods (no flag) | 7209 | ~77 MB | 3.8-10.9 s |
| deploy + sts + ds + rollouts (`--by-owner`) | 164 | ~700 kB | 0.6-1.0 s |

`BenchmarkReqlimByOwner` against `BenchmarkReqlim` shows the same shape on a
fake clientset, where the network cost is invisible and only the local walk
remains: 0.55ms / 1.18MB against 21.5ms / 43.7MB - the controller list is
simply the smaller collection to walk, before any network is involved.

The trade is what the row means, not a perf caveat: with `--by-owner` it is
the workload's **desired** spec (a rollout in flight shows only the new
one, a pod mutated after creation is invisible); without it, it is what is
actually **running**. Neither mode replaces the other.

**Also push filters server-side** where a field selector exists: `defaultsa`,
`on-node`, `pending` and `nodeips` do. There is no selector for
`metadata.deletionTimestamp`, which is why `terminating` has to sweep every pod.

## Client configuration

`internal/kube/client.go` sets two things and deliberately leaves a third alone:

- **`cfg.QPS = -1`** - client-go defaults a kubeconfig-built config to QPS 5 /
  Burst 10, sized for a long-running controller, with the limiter shared across
  the concurrent lists a view issues. Paging 6500 pods is 4 requests: 606ms with
  the defaults versus 44ms without, against a local server with no network
  latency. Guarded by `TestNoClientSideThrottling` - restoring the default breaks
  nothing, it just makes every command slower, which is exactly the kind of
  regression a test has to catch.
- **`cfg.Timeout`** - bounds the wait on a hung control plane.
- **Not `ContentType`** - the typed clientset already negotiates protobuf. Forcing
  JSON measured 116.5 MiB / 5.4s against 85.7 MiB / 3.0s for the default, so
  there is nothing to tune.

## Page size: the biggest single lever

`ChunkSize` (`internal/kube/list.go`) is 2000, not the 500 kubectl uses.
Paging is **sequential** - each request needs the previous response's
continue token - so the page count is round trips in series, and it is the
largest cost in a one-shot CLI after the bytes themselves.

Measured on a 7209-pod GKE cluster, `reqlim -A --by-owner`, the two
binaries run back to back six times:

| `ChunkSize` | pages | wall clock (median) | peak RSS |
| --- | --- | --- | --- |
| 500 | 15 | ~4.8 s | 373-410 MiB |
| 1000 | 8 | ~3.7 s | - |
| 2000 | 4 | ~2.6 s | 351-397 MiB |
| 4000 | 2 | ~2.4 s | - |

kubectl's 500 buys a memory ceiling klens does not get anyway: it
accumulates the whole collection before rendering, so the page size barely
moves peak RSS. What a smaller page does buy here is round trips. 2000
keeps the bound that stops the apiserver materializing an unbounded
collection, and 4000 is past the knee.

`TestChunkSizeBounded` and `TestListAllUsesChunkSize` pin the two
properties that matter: the limit is set, and it reaches the request.

## The shared render path

`Table` is on the hot path of all 34 commands, so it is worth keeping tight:

- `Flush` measures the exact cell bytes during the width pass and does a single
  `Grow`. Letting the builder double its way up was 62% of the bytes in
  `BenchmarkTableFlush`.
- `Row` copies into slices carved from shared 4096-cell blocks, so a 20k-row
  table costs a few dozen allocations instead of 20k. Callers keep ownership of
  the slice they pass.
- `sortRows` extracts sort keys once per row rather than inside the comparator,
  which runs O(n log n) times.
- `visibleWidth` scans in one pass instead of going through a regexp, and is
  allocation-free.

Net on `BenchmarkTableFlush` (20k rows, 8 columns): 20061 -> 67 allocs/op
(-99.7%), -43.7% B/op, -13.6% sec/op.

## Bounding the burst

`cfg.QPS = -1` removes client-side throttling, which is right for a one-shot
CLI (see below) but leaves nothing capping concurrency - and klens has two
fan-out layers that *multiply*. `unused-config` issues 10 concurrent lists;
under a glob matching `MaxNamespaceFanout` (16) namespaces each of those fans
out again, so 160 requests hit the apiserver at the same instant. HTTP/2
multiplexes them onto one connection, so the client never notices; a shared
control plane does, and API Priority and Fairness starts queuing.

`MaxInFlight` (`internal/kube/list.go`) is a counting semaphore around each page
request in `listAll` - the one place every List in the binary passes through. It
is held for a request, not for a whole paged walk, so a few large collections
cannot monopolize it.

Measured with `unused-config -n 'be-m*'` on a 7209-pod GKE cluster (10 lists x 7
namespaces = 70 requests unbounded), medians over four runs:

| `MaxInFlight` | wall clock |
| --- | --- |
| 16 | 1.53 s |
| 32 | 0.98 s |
| 48 | 0.97 s |
| 64 | 1.01 s |
| unbounded | 0.82 s |

32 is the knee: 16 costs 60%, 48 buys nothing over 32. It caps the worst-case
burst at a fifth of what it was for ~15% on a command that was already fast.
`TestListAllBoundsInFlight` guards it, and fails if the requests stop
overlapping at all - a bound that never binds would make the test vacuous.

## Measured and rejected

Do not re-litigate these without a new measurement:

| Tried | Result |
| --- | --- |
| Preallocating the view row slices and index maps | No significant change in sec/op (p=0.13-0.82). Worse on filtering views, which then size for rows they discard. |
| `GOGC=off` for the process | -8% on `Restarts`, nothing on `PvcResize` (p=0.84). Not worth an unbounded RSS on a large cluster, for 8% of the ~1% of wall time that is local CPU. |
| Padding without `strings.Repeat` in `writeLine` | Already free: the 67 remaining allocations in `Flush` show the compiler stack-allocates it. |
| Forcing protobuf explicitly | No-op, already negotiated. |
| Deferring `terminating`'s node list | ~10% of a command dominated by its pod list. Below the bar for the added branch. |
| `sync.Pool` anywhere | Nothing is reused across requests in a process that exits in seconds. |
| Passing `kube.Flags` by value to a per-row helper | Measured +3.6% on `BenchmarkReqlim` (p=0.015, identical B/op and allocs/op) against passing the one field it read. `Flags` is a ~120-byte struct; anything called once per object takes the field, not the struct - see `Command.ByOwner`'s note in CLAUDE.md's subcommand checklist. |
