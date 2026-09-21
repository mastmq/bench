# Results

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
