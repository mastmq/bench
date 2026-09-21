package scenario

import (
	"context"
	"fmt"
	"time"

	"github.com/mastmq/bench/internal/domain/report"
	"github.com/mastmq/bench/internal/infra/load"
	"github.com/urfave/cli/v3"
)

// Defaults for the fan-out scenario: enough subscribers that the per-message
// fan-out dominates, at a rate low enough that it is the fan-out being
// measured rather than ingest.
const (
	defaultFanoutSubscribers = 500
	defaultFanoutRate        = 10
	defaultFanoutSize        = 256
	defaultFanoutDuration    = 30 * time.Second
)

// fanoutCommand measures what one publisher costs when many clients are
// listening.
//
// It is a different question from throughput: a broker that handles 10k
// messages a second from a hundred publishers to a hundred subscribers may
// struggle badly with one publisher and ten thousand subscribers, because
// the work per message is the fan-out, not the parse.
func fanoutCommand() *cli.Command {
	return &cli.Command{
		Name:  "fanout",
		Usage: "one publisher to many subscribers, measuring delivery latency",
		Flags: append(commonFlags(),
			&cli.IntFlag{Name: "subscribers", Value: defaultFanoutSubscribers},
			&cli.IntFlag{Name: "rate", Usage: "messages per second", Value: defaultFanoutRate},
			&cli.IntFlag{Name: "size", Value: defaultFanoutSize},
			&cli.DurationFlag{Name: "duration", Value: defaultFanoutDuration},
			&cli.StringFlag{Name: "topic", Value: "bench/fanout"},
		),
		Action: runFanout,
	}
}

func runFanout(ctx context.Context, cmd *cli.Command) error {
	cfg := configFrom(cmd)
	subs := cmd.Int("subscribers")
	rate := cmd.Int("rate")
	duration := cmd.Duration("duration")
	topic := cmd.String("topic")

	collector := load.NewCollector(subs * rate * int(duration.Seconds()))

	subClients, err := startSubscribers(cfg, subs, topic, collector)
	if err != nil {
		return err
	}

	defer disconnectAll(subClients)

	time.Sleep(time.Second)

	sent, elapsed, errs := publish(ctx, cfg, 1, rate, cmd.Int("size"), duration, topic)

	time.Sleep(settleDelay)

	delivered := collector.Received()

	res := report.Result{
		Scenario: fmt.Sprintf("fanout 1pub x%d/s -> %dsub qos%d", rate, subs, cfg.QoS),
		Broker:   cfg.Broker,
		Started:  time.Now().Add(-elapsed),
		Duration: elapsed,
		Clients:  subs + 1,
		Sent:     sent,
		Received: delivered,
		Expected: sent * int64(subs),
		Errors:   errs,
		Latency:  report.Summarise(collector.Samples()),
		Notes: []string{
			fmt.Sprintf("expected %d deliveries (%d sent x %d subscribers), saw %d",
				sent*int64(subs), sent, subs, delivered),
		},
	}

	return emit(cmd, res)
}
