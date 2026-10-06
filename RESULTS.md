# Results

## 2026-10-05: the wildcard control plane, measured properly this time

Local core and two edges on the same laptop as the September runs, comparing `cdf8285` (one claim subscription per connection) and `b04e6c1` (one `mast.session.>` subscription per node, plus the hot-path lock work). Load: `mastbench connect`, 8,000 clean connections to one edge with the other edge idle, which is the phase that relays every claim to the other node.

| 8k connections to one edge, 60s idle before each run | cdf8285 | b04e6c1 |
| --- | --- | --- |
| round 1 | 1,863/s | 1,898/s |
| round 2 | 11,146/s | 2,645/s |
| round 3 | 2,869/s | 10,920/s |
| failures, warnings on any node | 0, 0 | 0, 0 |
| edge RSS holding 8k | 333–404MB | 311–344MB |
| NATS subscriptions relayed onto the idle edge | ~8,000 | 0 |

The spread is the laptop: both builds swing between 1.9k and 11k connections a second from one round to the next, and nothing separates them. What does separate them is what the idle edge holds for the busy one's clients, which went from one subscription and one goroutine per connection to nothing, and about 15% of resident memory with it.

**The September entry's "6.5× slower" for this design was the harness, and so were three more false regressions today.** Storming a fresh cluster within seconds of killing the previous one gave 319/8,000 on one run and 8,000/8,000 on the next, on whichever build ran second; a `pkill -f` pattern that did not match the renamed binaries left the old core alive for an hour, so later edges joined it and then lost its store directory; and `wait` without a pid waited on the brokers. With exact-name kills, sequential start, health gates and a minute of idle, every run on both builds was clean. The method is written up in the mast repo's notes; the lesson for this file is that a storm result which does not reproduce after an idle gap is a measurement of the previous run's teardown.

Storming both edges at once, 4k each, was noisy on both builds for a reason that is real and shared: a clean CONNECT costs the edge a KV read through the leaf connection, so 8k connects in a few seconds put 8k reads on one link, the durable consumer missed heartbeats and some reads hit the 5s store timeout (`dropping session ... context deadline exceeded`). That is the per-connect store traffic, not the control plane, and it is the next thing worth measuring.

### ode, single standalone pod, after the deploy

`b04e6c1` in the `baly-ode-central` namespace from a Job on the internal listener, with two caveats that bound every number here. The namespace LimitRange puts a **200m CPU limit** on any container that does not set one, which includes the broker (the chart sets a request and no limit) and the bench pod; and the namespace quota had 516m of CPU limit left, so the bench pod could be raised to 500m and no further.

| Shape | Result |
| --- | --- |
| 5,000 connections | 647/s then 1,058/s, 0 failures (September: 1,246/s) |
| 10 pub × 100/s → 10 subs, bench at 200m | 0% loss, p50 1.3ms, p95 301ms, **p99 1.15s** |
| same, bench at 500m | 0% loss, p50 1.2ms, p95 16.6ms, **p99 422ms** |
| 1 pub × 100/s → 100 subs, bench at 200m | 0% loss, p50 1.7ms, p99 977ms |
| 20 pub × 100/s → 20 subs, QoS 1 | 531/s achieved of 2,000, 0% loss, p50 64ms (September: 552/s) |
| 20 pub × 100/s → 20 subs, QoS 0, 40k deliveries/s into one pod | 29% loss, p50 10s: the generator pod, broker at 63m CPU |

Giving the generator 2.5× the CPU took the p95 from 301ms to 17ms and halved the p99, so the tail is throttling, and what remains is split between a bench pod still capped at 500m and a broker capped at 200m. The broker never exceeded 137m and ended every run with zero slow consumers, zero NATS disconnects and zero durable-store failures. Held connections cost about 30KB each on this build against 57KB in September. To measure the broker here rather than the namespace, the chart values need an explicit `core.resources.limits.cpu`, and the quota needs room for it.

**Rerun on 2026-10-06 with the generator at 2 CPU**, once the quota had room, which removes the generator as the limit:

| Shape | bench 200m (10-05) | bench 2 CPU (10-06) |
| --- | --- | --- |
| 5,000 connections | 647/s, 1,058/s | 939/s, 793/s, 0 failures |
| 10 pub × 100/s → 10 subs | p50 1.3ms, p99 1.15s, 0% loss | p50 1.3ms, p95 509ms, **p99 1.30s**, 0% loss |
| 1 pub × 100/s → 100 subs | p50 1.7ms, p99 977ms, 0% loss | p50 1.6ms, p95 846ms, **p99 1.41s**, 0% loss |
| 20 pub × 100/s → 20 subs, QoS 1 | 531/s, p50 64ms | p50 85ms, p99 544ms, 0% loss |
| 20 pub × 100/s → 20 subs, QoS 0 | 29% loss, p50 10s | **17% loss, p50 4.6s** |

This corrects the paragraph above. Quadrupling the generator's CPU again did not move the tail at 20k deliveries a second, so yesterday's improvement at 500m was mostly run-to-run noise, and the second of p99 belongs to the broker. Its 200m limit is a CFS quota of 20ms of CPU per 100ms period; a broker averaging 30–110m spends that quota in bursts and then sits throttled for the rest of the period, which is exactly a fast median with a tail in the hundreds of milliseconds. The 40k-deliveries/s run still loses messages with a generator that now has headroom, and that is the broker's QoS 0 back-pressure at 200m dropping what it cannot write. None of this is a property of mast; it is what one fifth of a core looks like under bursty fan-out. The broker ended every run Running with no restarts.

## 2026-09-28: QoS 1 and 2 on the durable stream

Same setup as the entry below: one core and two edges on one laptop, publishers on one edge and ten subscribers on the other. This measures [#11](https://github.com/mastmq/mast/issues/11)'s fix, which stores every QoS 1 and 2 publish on a replicated JetStream stream before acknowledging it and has each node read the stream through one consumer of its own.

| Cross-node, 10 subscribers | core NATS only (before) p50 / p99 | durable stream p50 / p99 | Loss |
| --- | --- | --- | --- |
| QoS 1, 10 pub × 200/s (20k deliveries/s) | 0.53ms / 2.3ms | 0.64ms / 1.7ms | 0% / 0% |
| QoS 1, 50 pub × 200/s (100k deliveries/s) | 0.94ms / 23ms | 12.8ms / 130ms | 0% / 0% |
| QoS 0, 10 pub × 200/s | 0.61ms / 2.6ms | 0.73ms / 3.5ms | 0% / 0% |

QoS 0 never touches the stream and does not move.

The first version of the consumer acknowledged every message and pulled the default 500 at a time, and at 100k deliveries a second it built a backlog: p50 **395ms**, p99 650ms, nothing lost. Neither the core (about 70% of one CPU) nor the receiving edge was saturated, which pointed at pacing rather than capacity. Acknowledging cumulatively — one `AckAll` every 64 messages or 100ms — and pulling 2,000 at a time brought it to the figures above.

What these runs do not show: the guarantee itself. That is a test rather than a benchmark — `TestClusterQoS1SurvivesALeafOutage` cuts an edge off from the core, publishes twenty QoS 1 messages on the other edge, and requires exactly twenty to arrive. Before the fix it received none.

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
