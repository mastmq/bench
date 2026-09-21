// Package report turns raw measurements into a result a person can act on.
//
// The numbers here are deliberately percentiles rather than averages. A mean
// latency hides exactly the behaviour a broker is bought for: an average of
// 3ms is compatible with one message in a hundred taking a second, and it is
// that one message that wakes somebody up.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"time"
)

// Result is the outcome of one scenario.
type Result struct {
	Scenario string        `json:"scenario"`
	Broker   string        `json:"broker"`
	Started  time.Time     `json:"started"`
	Duration time.Duration `json:"duration_ns"`

	// Clients is how many connections the scenario held open.
	Clients int `json:"clients"`
	// Sent and Received count messages, so a gap between them is visible
	// rather than averaged away.
	Sent     int64 `json:"sent"`
	Received int64 `json:"received"`
	Errors   int64 `json:"errors"`

	// Expected is how many deliveries should have occurred. It is not the
	// same as Sent: with N subscribers on a topic, one publish is N
	// deliveries, and comparing against Sent reports a 400% surplus rather
	// than no loss at all.
	Expected int64 `json:"expected"`

	// Latency percentiles, populated when the scenario measures them.
	Latency *Latency `json:"latency,omitempty"`

	// Notes records anything that qualifies the numbers.
	Notes []string `json:"notes,omitempty"`
}

// The percentiles reported. p99 is included because it is where a broker's
// bad behaviour actually lives: a p50 tells you the common case, which is
// rarely the case anybody is paged about.
const (
	p50 = 0.50
	p95 = 0.95
	p99 = 0.99

	percent = 100
)

// Latency holds the distribution of observed delays.
type Latency struct {
	Min time.Duration `json:"min_ns"`
	P50 time.Duration `json:"p50_ns"`
	P95 time.Duration `json:"p95_ns"`
	P99 time.Duration `json:"p99_ns"`
	Max time.Duration `json:"max_ns"`
}

// Rate returns messages per second over the run.
func (r Result) Rate() float64 {
	if r.Duration <= 0 {
		return 0
	}

	return float64(r.Sent) / r.Duration.Seconds()
}

// Loss returns the fraction of expected deliveries that never arrived.
//
// A negative result would mean more arrived than were asked for, which is a
// duplicate-delivery bug rather than negative loss, so it is reported as
// such by the caller rather than hidden by a clamp.
func (r Result) Loss() float64 {
	expected := r.Expected
	if expected == 0 {
		expected = r.Sent
	}

	if expected == 0 {
		return 0
	}

	return float64(expected-r.Received) / float64(expected)
}

// Summarise computes percentiles from raw samples.
//
// It sorts a copy: a caller that collected samples concurrently should not
// have its slice reordered underneath it.
func Summarise(samples []time.Duration) *Latency {
	if len(samples) == 0 {
		return nil
	}

	sorted := slices.Clone(samples)
	slices.Sort(sorted)

	return &Latency{
		Min: sorted[0],
		P50: percentile(sorted, p50),
		P95: percentile(sorted, p95),
		P99: percentile(sorted, p99),
		Max: sorted[len(sorted)-1],
	}
}

// percentile picks the nearest-rank value from an already sorted slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	idx = max(0, min(idx, len(sorted)-1))

	return sorted[idx]
}

// WriteText prints a result for a human reading a terminal.
func (r Result) WriteText(w io.Writer) error {
	out := func(format string, args ...any) error {
		_, err := fmt.Fprintf(w, format, args...)

		return err //nolint:wrapcheck // the caller reports the write failure
	}

	if err := out("\n%s against %s\n", r.Scenario, r.Broker); err != nil {
		return err
	}

	if err := out("  duration   %s\n  clients    %d\n", r.Duration.Round(time.Millisecond), r.Clients); err != nil {
		return err
	}

	return r.writeBody(out)
}

// WriteJSON prints a result for something that will read it later.
func (r Result) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("report: encoding result: %w", err)
	}

	return nil
}

// writeBody prints the parts of a result that are only present for some
// scenarios.
func (r Result) writeBody(out func(string, ...any) error) error {
	if r.Sent > 0 {
		expected := r.Expected
		if expected == 0 {
			expected = r.Sent
		}

		if err := out("  sent       %d (%.0f/s)\n  delivered  %d of %d expected\n  loss       %.2f%%\n",
			r.Sent, r.Rate(), r.Received, expected, r.Loss()*percent); err != nil {
			return err
		}
	}

	if r.Errors > 0 {
		if err := out("  errors     %d\n", r.Errors); err != nil {
			return err
		}
	}

	if l := r.Latency; l != nil {
		if err := out("  latency    p50 %s  p95 %s  p99 %s  max %s\n",
			l.P50.Round(time.Microsecond), l.P95.Round(time.Microsecond),
			l.P99.Round(time.Microsecond), l.Max.Round(time.Microsecond)); err != nil {
			return err
		}
	}

	for _, n := range r.Notes {
		if err := out("  note       %s\n", n); err != nil {
			return err
		}
	}

	return nil
}
