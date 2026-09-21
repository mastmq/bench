// Package load drives MQTT clients against a broker.
//
// Two decisions shape everything here.
//
// Publishing is paced against a fixed schedule rather than by sleeping
// between sends. Sleeping makes a slow broker look fast: the loop simply
// sends less, the measured latency stays low, and the number that matters —
// how far behind the intended rate the system fell — disappears. This is
// coordinated omission, and correcting for it is the difference between a
// benchmark and a reassurance.
//
// Latency is measured from a timestamp inside the payload, so it spans the
// whole path a message actually takes: publisher, broker, and subscriber.
// Measuring the publish call alone would time the local socket write.
package load

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Config is what every scenario needs to reach the broker.
type Config struct {
	Broker   string
	Username string
	Password string
	// ClientPrefix distinguishes this run's clients from anything else
	// connected, which matters when benchmarking a shared environment.
	ClientPrefix string
	QoS          byte
	Timeout      time.Duration
}

// DisconnectQuiesce is how long a client waits for in-flight work before
// closing. Short: a benchmark's clients have nothing worth draining.
const DisconnectQuiesce = 100

// timestampBytes is the width of the nanosecond timestamp each payload
// carries in front of its filler.
const timestampBytes = 8

// Errors reported by this package.
var (
	// ErrConnect is returned when a client cannot reach the broker.
	ErrConnect = errors.New("load: connect failed")
	// ErrSubscribe is returned when a subscription is refused.
	ErrSubscribe = errors.New("load: subscribe failed")
)

// Connect opens one client.
//
//nolint:ireturn // paho.Client is an interface in the library, not a type.
func Connect(cfg Config, id string) (paho.Client, error) {
	opts := paho.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(id).
		SetUsername(cfg.Username).
		SetPassword(cfg.Password).
		SetConnectTimeout(cfg.Timeout).
		SetAutoReconnect(false).
		SetCleanSession(true)

	client := paho.NewClient(opts)

	token := client.Connect()
	if !token.WaitTimeout(cfg.Timeout) || token.Error() != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrConnect, id, token.Error())
	}

	return client, nil
}

// Payload builds a message of the requested size carrying the send time.
//
// The timestamp is in the payload rather than an MQTT 5 property so the same
// harness works against a v3.1.1 broker.
func Payload(size int, sent time.Time) []byte {
	if size < timestampBytes {
		size = timestampBytes
	}

	buf := make([]byte, size)
	binary.BigEndian.PutUint64(buf, uint64(sent.UnixNano()))

	return buf
}

// LatencyOf recovers how long a payload took to arrive. It returns false for
// anything too short to carry a timestamp, which is how traffic that is not
// ours is ignored.
func LatencyOf(payload []byte, now time.Time) (time.Duration, bool) {
	if len(payload) < timestampBytes {
		return 0, false
	}

	sent := int64(binary.BigEndian.Uint64(payload)) //nolint:gosec // round-trips our own value

	return now.Sub(time.Unix(0, sent)), true
}

// Collector gathers latency samples from concurrent subscribers.
type Collector struct {
	mu       sync.Mutex
	samples  []time.Duration
	received atomic.Int64
}

// NewCollector preallocates room for the expected number of samples.
func NewCollector(expected int) *Collector {
	return &Collector{
		mu:       sync.Mutex{},
		samples:  make([]time.Duration, 0, expected),
		received: atomic.Int64{},
	}
}

// Observe records one delivery.
func (c *Collector) Observe(latency time.Duration) {
	c.received.Add(1)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.samples = append(c.samples, latency)
}

// Received returns how many deliveries were seen.
func (c *Collector) Received() int64 { return c.received.Load() }

// Samples returns a copy of the observations.
func (c *Collector) Samples() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]time.Duration, len(c.samples))
	copy(out, c.samples)

	return out
}

// Pace calls send on a fixed schedule for the given duration and reports how
// many sends were issued.
//
// It does not sleep for the interval after each send; it sleeps until the
// next scheduled instant. If the broker slows down, the schedule does not,
// and the backlog shows up in latency instead of quietly reducing the rate.
func Pace(ctx context.Context, rate int, duration time.Duration, send func(seq int64)) int64 {
	if rate <= 0 {
		return 0
	}

	interval := time.Second / time.Duration(rate)
	deadline := time.Now().Add(duration)

	var seq int64

	next := time.Now()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return seq
		default:
		}

		send(seq)
		seq++

		next = next.Add(interval)

		if wait := time.Until(next); wait > 0 {
			time.Sleep(wait)
		}
	}

	return seq
}
