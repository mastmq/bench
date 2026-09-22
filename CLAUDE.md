# bench

Load and latency benchmarks for MQTT brokers. Named for mast, but **nothing here is mast-specific** — it runs against any broker, and keeping it that way is the point. A benchmark that only measures your own broker measures nothing.

Go, module `github.com/mastmq/bench`, binary `mastbench`.

## Why this exists instead of an off-the-shelf tool

Both reasons were learned by trying.

**Coordinated omission.** Most benchmark tools sleep between sends. That makes a slow broker look fast: the loop issues fewer messages, measured latency stays flat, and the number that actually matters — how far behind the intended rate the system fell — disappears. `internal/infra/load` paces against a **fixed schedule**, so a backlog shows up as latency rather than as a quietly reduced rate.

**If you change the pacing to sleep-between-sends, you have deleted the reason this tool exists.**

**Knowing what failed.** An ad-hoc run against a real cluster reported zero messages and looked like a broker fault. The real cause was `unknown option '-s'` in a CLI flag. A harness you can read beats one you have to trust.

## Correctness rules for the reporting

**`expected` counts deliveries, not publishes.** With five subscribers, one publish is five deliveries. Comparing arrivals against `sent` reports a 400% surplus rather than no loss. That field exists specifically to prevent that bug — do not "simplify" it away.

**Percentiles, never averages.** A mean of 3ms is perfectly compatible with one message in a hundred taking a second, and it is that message somebody gets paged about. Report p50/p95/p99/max.

**Latency is measured from a timestamp inside the payload**, so it covers publisher → broker → subscriber, not the local socket write.

## The three scenarios ask different questions

| Scenario | Question |
| --- | --- |
| `connect` | how fast can N connections be established, and what does steady-state memory look like while they are held |
| `throughput` | at a driven rate, what arrived and how late |
| `fanout` | one publisher to many subscribers |

`connect` is the one that matters when replacing a broker: the cutover *is* a reconnect storm, because everything that was connected arrives at once. It reports connections per second, and holding the connections is what lets you watch steady-state memory rather than the peak during the dial.

`fanout` is not a variant of `throughput`. A broker comfortable with 10k msg/s across a hundred subscribers can struggle badly with one publisher and ten thousand, because the per-message work is the fan-out, not the parse.

Every scenario takes `--qos 0|1|2` and `--json`. New scenarios keep both.

## Running it honestly

**Run it inside the cluster.** A `kubectl port-forward` funnels every client through one tunnel, so you measure the tunnel. `deployments/job.yaml` is there for this.

**If the broker authenticates over HTTP, a connection storm is a load test of the auth service, not the broker.** mast's own deployment sits behind one limited to 10m of CPU, which a few hundred connections per second will flatten. Use an unauthenticated internal listener for connection-scale work if the broker has one, and say which you used when reporting a number.

**A run against a shared namespace is visible to everyone else in it.** `--prefix` exists so your clients are identifiable in someone else's logs. Use it.

## RESULTS.md

Records real runs. Its existing entries include a correction — the first headline number was wrong, and the note explaining why is more valuable than the number was. Keep that habit: when a result turns out to be measuring the harness, the tunnel or the auth service, write down what it was actually measuring rather than quietly replacing the figure.

Always record the broker version, the shape it was running in, where the load generator ran, and the QoS.

## Layout and tooling

Same conventions as `mastmq/mast`: `cmd/` → `internal/cmd` → `internal/domain` (pure) + `internal/infra` (I/O), golangci-lint v2 with `default: all`, `just test` / `just lint` / `just smoke`.

`just smoke` runs a short `connect` and `throughput` pair against `tcp://127.0.0.1:1883`, which is the fastest way to confirm a change did not break the harness itself.

## Conventions

Conventional commits, body in prose explaining why. Markdown one paragraph per line.
