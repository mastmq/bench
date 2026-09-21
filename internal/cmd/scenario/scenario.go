// Package scenario implements the benchmark scenarios.
package scenario

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/mastmq/bench/internal/domain/report"
	"github.com/mastmq/bench/internal/infra/load"
	"github.com/urfave/cli/v3"
)

// Defaults for the connect scenario and for every scenario's transport.
const (
	defaultClients     = 1000
	defaultHold        = 30 * time.Second
	defaultConcurrency = 50
	defaultTimeout     = 10 * time.Second
)

// Commands returns every scenario.
func Commands() []*cli.Command {
	return []*cli.Command{connectCommand(), throughputCommand(), fanoutCommand()}
}

// commonFlags are shared by every scenario.
func commonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "broker",
			Usage:   "broker address, for example tcp://emq-emqx:1883",
			Value:   "tcp://127.0.0.1:1883",
			Sources: cli.EnvVars("BENCH_BROKER"),
		},
		&cli.StringFlag{
			Name:    "username",
			Usage:   "username, or the token these deployments carry there",
			Sources: cli.EnvVars("BENCH_USERNAME"),
		},
		&cli.StringFlag{
			Name:    "password",
			Sources: cli.EnvVars("BENCH_PASSWORD"),
		},
		&cli.StringFlag{
			Name:  "prefix",
			Usage: "client id prefix, so this run is distinguishable from real traffic",
			Value: "bench",
		},
		&cli.IntFlag{
			Name:  "qos",
			Usage: "0, 1 or 2",
			Value: 0,
		},
		&cli.DurationFlag{
			Name:  "timeout",
			Value: defaultTimeout,
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "emit the result as JSON instead of text",
		},
	}
}

func configFrom(cmd *cli.Command) load.Config {
	return load.Config{
		Broker:       cmd.String("broker"),
		Username:     cmd.String("username"),
		Password:     cmd.String("password"),
		ClientPrefix: cmd.String("prefix"),
		QoS:          byte(cmd.Int("qos")), //nolint:gosec // validated by the flag's range in practice
		Timeout:      cmd.Duration("timeout"),
	}
}

func emit(cmd *cli.Command, res report.Result) error {
	if cmd.Bool("json") {
		return res.WriteJSON(os.Stdout)
	}

	return res.WriteText(os.Stdout)
}

// connectCommand measures how fast connections can be established and how
// many can be held.
//
// This is the scenario that matters most for a broker replacing another one,
// because the cutover itself is a reconnect storm: every client that was
// connected arrives at once.
func connectCommand() *cli.Command {
	return &cli.Command{
		Name:  "connect",
		Usage: "open N connections as fast as possible and hold them",
		Flags: append(commonFlags(),
			&cli.IntFlag{Name: "clients", Value: defaultClients},
			&cli.DurationFlag{Name: "hold", Value: defaultHold},
			&cli.IntFlag{Name: "concurrency", Usage: "parallel dials", Value: defaultConcurrency},
		),
		Action: runConnect,
	}
}

// dialAll opens n connections with bounded parallelism and reports how long
// it took, which is the number that matters during a reconnect storm.
func dialAll(cfg load.Config, n, concurrency int) ([]paho.Client, int64, time.Duration) {
	var (
		mu      sync.Mutex
		clients []paho.Client
		errs    int64
		sem     = make(chan struct{}, concurrency)
		wg      sync.WaitGroup
	)

	start := time.Now()

	for i := range n {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			c, err := load.Connect(cfg, fmt.Sprintf("%s-conn-%d", cfg.ClientPrefix, i))

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				errs++

				return
			}

			clients = append(clients, c)
		}(i)
	}

	wg.Wait()

	return clients, errs, time.Since(start)
}

func runConnect(ctx context.Context, cmd *cli.Command) error {
	cfg := configFrom(cmd)
	want := cmd.Int("clients")

	clients, errs, elapsed := dialAll(cfg, want, cmd.Int("concurrency"))

	res := report.Result{
		Scenario: fmt.Sprintf("connect x%d", want),
		Broker:   cfg.Broker,
		Started:  time.Now().Add(-elapsed),
		Duration: elapsed,
		Clients:  0,
		Sent:     0,
		Received: 0,
		Errors:   0,
		Expected: 0,
		Latency:  nil,
		Notes:    nil,
	}
	res.Clients = len(clients)
	res.Errors = errs
	res.Notes = append(res.Notes,
		fmt.Sprintf("established %d/%d in %s (%.0f conn/s)",
			len(clients), want, res.Duration.Round(time.Millisecond),
			float64(len(clients))/res.Duration.Seconds()))

	if err := emit(cmd, res); err != nil {
		return err
	}

	// Hold them, so whoever is watching the broker sees steady-state memory
	// rather than the peak during a dial storm.
	if _, err := fmt.Fprintf(cmd.Root().Writer, "\nholding %d connections for %s\n",
		len(clients), cmd.Duration("hold")); err != nil {
		return fmt.Errorf("scenario: writing progress: %w", err)
	}

	select {
	case <-time.After(cmd.Duration("hold")):
	case <-ctx.Done():
	}

	for _, c := range clients {
		c.Disconnect(load.DisconnectQuiesce)
	}

	return nil
}
