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
16. The fake clientset understates the win: a real targeted List also skips
the bytes and the protobuf decode of every namespace it did not match.

**Also push filters server-side** where a field selector exists: `defaultsa`,
`on-node`, `pending` and `nodeips` do. There is no selector for
`metadata.deletionTimestamp`, which is why `terminating` has to sweep every pod.

## Client configuration

`internal/kube/client.go` sets two things and deliberately leaves a third alone:

- **`cfg.QPS = -1`** - client-go defaults a kubeconfig-built config to QPS 5 /
  Burst 10, sized for a long-running controller, with the limiter shared across
  the concurrent lists a view issues. Paging 6500 pods is 13 requests: 606ms with
  the defaults versus 44ms without, against a local server with no network
  latency. Guarded by `TestNoClientSideThrottling` - restoring the default breaks
  nothing, it just makes every command slower, which is exactly the kind of
  regression a test has to catch.
- **`cfg.Timeout`** - bounds the wait on a hung control plane.
- **Not `ContentType`** - the typed clientset already negotiates protobuf. Forcing
  JSON measured 116.5 MiB / 5.4s against 85.7 MiB / 3.0s for the default, so
  there is nothing to tune.

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
