# Results

## 2026-09-27: a local cluster, before and after the September fixes

mast running as one `core` and two `edge` processes on one laptop (8 cores, 16GB, macOS), each edge joined to the core as a leaf node, load from `mastbench` on the same machine against the edges' unauthenticated internal listeners. Every throughput and fanout run crosses nodes: publishers on one edge, subscribers on the other, via the new `--sub-broker` flag. Compared: `250ffcc`, before the delivery fixes, and `3099db8`, after them and after the session-cost fixes. Numbers from one machine are for comparing builds, not for quoting as capacity.

### Delivery: the fixes cost nothing measurable

| Cross-node, QoS 0 unless noted | 250ffcc p50 / p99 | 3099db8 p50 / p99 | Loss |
| --- | --- | --- | --- |
| 10 pub × 200/s → 10 sub (20k deliveries/s) | 695µs / 3.0ms | 1.07ms / 4.5ms | 0% / 0% |
| 10 pub × 1000/s → 10 sub (100k/s) | 260µs / 1.6ms | 257µs / 2.2ms | 0% / 0% |
| 10 pub × 2000/s → 10 sub (200k/s) | 450µs / 4.7ms | 438µs / 5.5ms | 0% / 0% |
| 10 pub × 200/s → 10 sub, QoS 1 | 617µs / 2.2ms | 716µs / 2.2ms | 0% / 0% |
| 1 pub × 10/s → 5000 sub | 23ms / 49ms | 24ms / 48ms | 0% / 0% |

The differences are inside run-to-run noise. The per-message id, the duplicate check and the shared-group routing added in `14ab8bd` do not show up.

### Reconnect storm: fast, once the machine stops being the limit

| 12k clean connections to one edge | Rate | KV read through the edge, mid-storm, p50 / p99 |
| --- | --- | --- |
| 250ffcc | 7,456/s | 1.13ms / 6.4ms |
| 3099db8 | 8,352/s | 1.04ms / 5.1ms |

Zero failures and zero store timeouts in both. The gain is `17c64e9`: a clean session no longer writes to the store on SUBSCRIBE, and deleting a session reads first instead of writing a tombstone for a key that was never there.

Held connections cost about 45KB and three goroutines each on the edge: mochi's read and write loops, and the nats.go delivery goroutine behind each connection's session-claim subscription. Every edge also holds every other edge's claim subscriptions, because interest is relayed to all of them — 12k clients on one edge put 12k subscriptions on the other.

### The measurement that mattered, again: 16,384

Storming both edges at once, 10k clients each, "failed" about 18% of connections in every run and every build, and it was taken for a broker limit for longer than it should have been. The tell was that the totals never moved: 16,327, 16,333, 16,315 and so on, against 16,384 — the size of macOS's ephemeral port range, 49152–65535, which every outgoing connection on the machine shares regardless of destination. The failed dials were never sent; they waited out their ten-second timeout, which also dragged the reported connection rate down and starved `curl` of ports mid-run. **On one host, keep the total client count under about 16k, or widen `net.inet.ip.portrange.first`.**

### A hypothesis that was wrong

Before the port limit was found, slow KV reads through an edge during the dual storm (p99 25ms against 0.4ms idle) looked like head-of-line blocking on the leaf connection, behind the flood of per-connection claim subscriptions being relayed between edges. The proposed fix — one wildcard claim subscription per node instead of one per connection — was built and measured. It removed a goroutine and a subscription per connection and made the storm **6.5× slower** (1,274 connections a second) with KV reads at a p99 of 184ms, so it was reverted. Why broadcasting every claim to every node costs that much is not understood yet; until it is, per-connection claim subscriptions stay.

### Found along the way

The `core` role served no `/metrics`, `/healthz` or pprof at all: `broker.Start` returned for a core before starting the endpoint. Fixed in `3099db8`.

## 2026-09-21: one pod in shared staging

Measured against mast running as a single all-in-one pod in a shared OpenShift staging namespace, on 2026-09-21. The broker requested 1 CPU with a 1Gi memory limit and used `emptyDir` storage.

All load came from **one** generator pod. That turns out to be the most important sentence here.

## Headline

mast was never the bottleneck in any run. Read on before quoting a number.

| Scenario | Result |
| --- | --- |
| 5000 connections, internal listener | 4.0s, **1246 conn/s**, 0 failures |
| 2000 idle connections held | 8Mi → 123Mi, **~57KB per connection** |
| 1 pub → 1 sub @ 5000 msg/s | 0% loss, p50 12.5ms, p99 72ms |
| 1 pub → 100 subs @ 10k deliveries/s | 0% loss, p50 1.3ms, p99 23ms |
| 10 pub → 10 subs @ 10k deliveries/s | 0% loss, p50 949µs, p99 7.6ms |

## The measurement that mattered

The first large run looked alarming: 50 publishers at 200/s to 50 subscribers reported **96.8% loss** and a p50 latency of 32 seconds. Taken at face value that is a broker falling over.

It is not, and the tell is in the resource column. The broker sat at **63–115m of CPU** throughout. A broker that is dropping nine messages in ten because it cannot keep up is busy; this one was idle.

Two runs separated cause from coincidence, each demanding exactly 10,000 deliveries per second:

| Shape | Deliveries/s | Loss | p50 |
| --- | --- | --- | --- |
| 1 publisher → **100 subscribers** @ 100/s | 10,000 | **0%** | 1.3ms |
| 50 publishers → **1 subscriber** @ 10,000/s | 10,000 | **21%** | 645ms |

Identical aggregate load through the broker. The difference is entirely in how much one client had to absorb. Spread across a hundred subscribers it is effortless; concentrated on one it starts dropping.

A per-client ladder pins the knee:

| Rate to a single subscriber | Loss | p50 | p99 |
| --- | --- | --- | --- |
| 500/s | 0% | 560µs | 5.5ms |
| 1,000/s | 0% | 546µs | 26ms |
| 2,000/s | 0% | 526µs | 33ms |
| 5,000/s | 0% | 12.5ms | 72ms |
| 10,000/s | 21% | 645ms | 1.16s |

So the limit reached in these runs is **per-subscriber delivery**, somewhere between 5,000 and 10,000 messages per second to one client, and the earlier catastrophic numbers were every subscriber being asked to absorb that much at once.

Part of that ceiling is the broker's own back-pressure behaving as designed — `maxWritesPending` bounds a client's outbound queue and QoS 0 discards past it, which is what QoS 0 means. Part of it is the Go client in the generator. These runs cannot separate the two, because both sit behind the same single pod.

## QoS 1

| Shape | Achieved | Loss | p50 |
| --- | --- | --- | --- |
| 20 pub × 250/s target, 20 subs | **552 msg/s** of 5,000 asked | 0% | 95ms |

QoS 1 publishes wait for their PUBACK, so the publisher rate collapsed to a ninth of the target — and nothing was lost. That is the trade working correctly: QoS 1 converts what QoS 0 would have dropped into back-pressure on the publisher. The number to take from this is not 552/s, it is that the rate and the loss moved in opposite directions.

## What this does not tell you

**mast's actual ceiling is unknown.** It was never pushed past ~200m CPU or 828Mi of memory, and every apparent failure traced back to one generator pod. Finding the real limit needs load spread across several pods, which the harness cannot do yet — see the distributed-load issue.

**Nothing here touched authentication.** These runs used the unauthenticated internal listener deliberately, because the deployment's auth service is limited to 10m of CPU and a connection storm would have measured that instead. Connection rates through an authenticated listener will be far lower, and bounded by that service rather than by the broker.

**One pod, one namespace, one afternoon.** Shared staging, other tenants' work in flight, no repetition. Treat these as orders of magnitude, not benchmarks.
