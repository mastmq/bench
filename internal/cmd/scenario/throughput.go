package scenario

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/mastmq/bench/internal/domain/report"
	"github.com/mastmq/bench/internal/infra/load"
	"github.com/urfave/cli/v3"
)

// settleDelay lets in-flight messages arrive before the count is taken, so
// the tail of a run is not reported as loss.
const settleDelay = 3 * time.Second

// Defaults for the throughput scenario.
const (
	defaultPublishers  = 10
	defaultSubscribers = 10
	defaultRatePerPub  = 100
	defaultPayloadSize = 256
	defaultRunDuration = 30 * time.Second
)

// throughputCommand drives a target message rate and measures what actually
// arrives, and how late.
func throughputCommand() *cli.Command {
	return &cli.Command{
		Name:  "throughput",
		Usage: "publish at a target rate and measure delivery latency",
		Flags: append(commonFlags(),
			&cli.IntFlag{Name: "publishers", Value: defaultPublishers},
			&cli.IntFlag{Name: "subscribers", Value: defaultSubscribers},
			&cli.IntFlag{Name: "rate", Usage: "messages per second per publisher", Value: defaultRatePerPub},
			&cli.IntFlag{Name: "size", Usage: "payload bytes", Value: defaultPayloadSize},
			&cli.DurationFlag{Name: "duration", Value: defaultRunDuration},
			&cli.StringFlag{Name: "topic", Value: "bench/throughput"},
		),
		Action: runThroughput,
	}
}

func runThroughput(ctx context.Context, cmd *cli.Command) error {
	cfg := configFrom(cmd)
	pubs, subs := cmd.Int("publishers"), cmd.Int("subscribers")
	rate, size := cmd.Int("rate"), cmd.Int("size")
	duration := cmd.Duration("duration")
	topic := cmd.String("topic")

	expected := pubs * rate * int(duration.Seconds())
	collector := load.NewCollector(expected)

	subClients, err := startSubscribers(cfg, subs, topic, collector)
	if err != nil {
		return err
	}

	defer disconnectAll(subClients)

	// Interest has to propagate before the first publish, or the early
	// messages are counted as loss that never happened.
	time.Sleep(time.Second)

	sent, elapsed, errs := publish(ctx, cfg, pubs, rate, size, duration, topic)

	time.Sleep(settleDelay)

	res := report.Result{
		Scenario: fmt.Sprintf("throughput %dpub x%d/s -> %dsub qos%d", pubs, rate, subs, cfg.QoS),
		Broker:   cfg.Broker,
		Started:  time.Now().Add(-elapsed),
		Duration: elapsed,
		Clients:  pubs + subs,
		Sent:     sent,
		Received: collector.Received(),
		Expected: sent * int64(subs),
		Errors:   errs,
		Latency:  report.Summarise(collector.Samples()),
		Notes: []string{
			fmt.Sprintf("each message fans out to %d subscribers, so received is about sent x %d", subs, subs),
		},
	}

	return emit(cmd, res)
}

// startSubscribers connects the subscriber fleet and waits for every
// subscription to be acknowledged.
func startSubscribers(cfg load.Config, n int, topic string, collector *load.Collector) ([]paho.Client, error) {
	clients := make([]paho.Client, 0, n)

	for i := range n {
		c, err := load.Connect(cfg, fmt.Sprintf("%s-sub-%d", cfg.ClientPrefix, i))
		if err != nil {
			disconnectAll(clients)

			return nil, err
		}

		token := c.Subscribe(topic, cfg.QoS, func(_ paho.Client, m paho.Message) {
			if latency, ok := load.LatencyOf(m.Payload(), time.Now()); ok {
				collector.Observe(latency)
			}
		})
		if !token.WaitTimeout(cfg.Timeout) || token.Error() != nil {
			disconnectAll(clients)

			return nil, fmt.Errorf("%w: %s: %w", load.ErrSubscribe, topic, token.Error())
		}

		clients = append(clients, c)
	}

	return clients, nil
}

// publish runs the publisher fleet against a fixed schedule.
func publish(
	ctx context.Context,
	cfg load.Config,
	pubs, rate, size int,
	duration time.Duration,
	topic string,
) (int64, time.Duration, int64) {
	var (
		sent, errs atomic.Int64
		wg         sync.WaitGroup
	)

	start := time.Now()

	for i := range pubs {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			c, err := load.Connect(cfg, fmt.Sprintf("%s-pub-%d", cfg.ClientPrefix, i))
			if err != nil {
				errs.Add(1)

				return
			}

			defer c.Disconnect(load.DisconnectQuiesce)

			load.Pace(ctx, rate, duration, func(int64) {
				token := c.Publish(topic, cfg.QoS, false, load.Payload(size, time.Now()))
				if cfg.QoS > 0 && !token.WaitTimeout(cfg.Timeout) {
					errs.Add(1)

					return
				}

				sent.Add(1)
			})
		}(i)
	}

	wg.Wait()

	return sent.Load(), time.Since(start), errs.Load()
}

func disconnectAll(clients []paho.Client) {
	for _, c := range clients {
		c.Disconnect(load.DisconnectQuiesce)
	}
}
