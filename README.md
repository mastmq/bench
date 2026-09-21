# bench

Load and latency benchmarks for [mast](https://github.com/mastmq/mast). Any MQTT broker works; nothing here is mast-specific.

## Why not an off-the-shelf tool

Two reasons, both learned by trying.

**Pacing.** Most benchmark tools sleep between sends. That makes a slow broker look fast: the loop simply issues fewer messages, measured latency stays flat, and the number that matters — how far behind the intended rate the system fell — vanishes. This paces against a fixed schedule instead, so a backlog shows up as latency rather than as a quietly reduced rate. The name for the thing being avoided is coordinated omission.

**Knowing what failed.** An ad-hoc run against a real cluster reported zero messages and looked like a broker fault; the real cause was `unknown option '-s'` in a CLI flag. A harness you can read is worth more than one you have to trust.

## Scenarios

```console
$ mastbench connect    --broker tcp://broker:1883 --clients 5000 --concurrency 100
$ mastbench throughput --broker tcp://broker:1883 --publishers 50 --rate 200 --subscribers 50
$ mastbench fanout     --broker tcp://broker:1883 --subscribers 2000 --rate 20
```

**connect** opens N connections as fast as it can and holds them. This is the scenario that matters when replacing a broker, because the cutover is itself a reconnect storm: everything that was connected arrives at once. It reports connections per second and, while holding, lets you watch steady-state memory rather than the peak during the dial.

**throughput** drives a target rate and reports what arrived and how late. Latency is measured from a timestamp inside the payload, so it covers the whole path — publisher, broker, subscriber — rather than the local socket write.

**fanout** is one publisher to many subscribers. It asks a different question from throughput: a broker comfortable with 10k messages a second across a hundred subscribers can struggle badly with one publisher and ten thousand, because the per-message work is the fan-out, not the parse.

Every scenario takes `--qos 0|1|2` and `--json`.

## Reading the output

```
throughput 10pub x200/s -> 5sub qos0 against tcp://broker:1883
  duration   10.005s
  clients    15
  sent       20000 (1999/s)
  delivered  100000 of 100000 expected
  loss       0.00%
  latency    p50 744µs  p95 2.324ms  p99 3.953ms  max 13.014ms
```

`expected` is deliveries, not publishes: with five subscribers one publish is five deliveries. Comparing against `sent` would report a 400% surplus rather than no loss, which is exactly the bug this field exists to prevent.

Percentiles, not averages. A mean of 3ms is compatible with one message in a hundred taking a second, and it is that message somebody gets paged about.

## Running against a cluster

Run it inside the cluster. A `kubectl port-forward` funnels every client through one tunnel, so you end up measuring the tunnel.

```console
$ kubectl -n <namespace> apply -f deployments/job.yaml
$ kubectl -n <namespace> logs -f job/mastbench
```

Two cautions worth reading before pointing this at a shared environment.

If the broker authenticates over HTTP, every CONNECT reaches that service, and a connection storm is a load test of the auth service rather than the broker. Check what it is sized for first — mast's own deployment sits behind one limited to 10m of CPU, which a few hundred connections a second will flatten. Use an unauthenticated internal listener for connection-scale work if there is one.

And a benchmark run against a shared namespace is visible to everyone else in it. `--prefix` exists so your clients are identifiable in someone else's logs.
