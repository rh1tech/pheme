package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Measurement. Everything here is client-side: what a user would actually experience, rather than
// what the server believes about itself.

// latencies collects durations for percentile reporting. It keeps every sample rather than
// bucketing: a run produces at most a few million, and exact tails matter more than memory here —
// p99 is the number that decides whether the product feels broken.
type latencies struct {
	mu      sync.Mutex
	samples []time.Duration
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.samples = append(l.samples, d)
	l.mu.Unlock()
}

func (l *latencies) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.samples)
}

// summary returns p50, p95, p99 and max. A percentile of an empty set is zero, reported alongside
// a count of zero so it cannot be mistaken for a fast result.
func (l *latencies) summary() (p50, p95, p99, max time.Duration, n int) {
	l.mu.Lock()
	sorted := make([]time.Duration, len(l.samples))
	copy(sorted, l.samples)
	l.mu.Unlock()

	n = len(sorted)
	if n == 0 {
		return 0, 0, 0, 0, 0
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(q float64) time.Duration {
		idx := int(q * float64(n))
		if idx >= n {
			idx = n - 1
		}
		return sorted[idx]
	}
	return at(0.50), at(0.95), at(0.99), sorted[n-1], n
}

// counters tallies outcomes by name. Error bodies are keyed by status so a run that fails reports
// WHY rather than just how often.
type counters struct {
	mu sync.Mutex
	n  map[string]int64
}

func newCounters() *counters { return &counters{n: map[string]int64{}} }

func (c *counters) inc(key string) { c.add(key, 1) }

func (c *counters) add(key string, delta int64) {
	c.mu.Lock()
	c.n[key] += delta
	c.mu.Unlock()
}

func (c *counters) get(key string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[key]
}

func (c *counters) snapshot() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int64, len(c.n))
	for k, v := range c.n {
		out[k] = v
	}
	return out
}

// delivery tracks, per sent message, how many of the expected recipients actually received it over
// SSE and how long each took.
//
// This is the measurement that matters most and the one a naive load test omits. The server's live
// bus drops events on the floor when a subscriber's 16-slot buffer is full — no error, no
// backpressure, nothing in the response. A test that only measures POST latency would call that a
// perfect run at any load, while users silently lose messages.
type delivery struct {
	mu      sync.Mutex
	pending map[string]*inflight
	// orphans holds arrivals for a message the sender has not registered yet. The SSE event
	// routinely beats the POST response back to this process — delivery latency and send latency
	// are the same order of magnitude — so discarding unknown ids would invent losses that never
	// happened. The first version of this test did exactly that and reported 8 lost messages at 10
	// msg/s, which was the harness measuring itself.
	orphans  map[string][]time.Time
	e2e      *latencies
	expected int64
	received int64
}

type inflight struct {
	sentAt   time.Time
	want     int // recipients expected to see it
	got      int
	convID   string
	finished bool
}

func newDelivery() *delivery {
	return &delivery{
		pending: map[string]*inflight{},
		orphans: map[string][]time.Time{},
		e2e:     &latencies{},
	}
}

// sent records a message and how many streams should see it, then claims any arrivals that beat
// this call.
func (d *delivery) sent(msgID, convID string, want int, at time.Time) {
	d.mu.Lock()
	f := &inflight{sentAt: at, want: want, convID: convID}
	d.pending[msgID] = f
	d.expected += int64(want)
	early := d.orphans[msgID]
	delete(d.orphans, msgID)
	f.got += len(early)
	d.received += int64(len(early))
	d.mu.Unlock()

	for _, t := range early {
		d.e2e.add(t.Sub(at))
	}
}

// arrived records one stream receiving a message. An id this run has not registered yet is held
// as an orphan rather than dropped — see the note on the orphans field.
func (d *delivery) arrived(msgID string, at time.Time) {
	d.mu.Lock()
	f, ok := d.pending[msgID]
	if !ok {
		d.orphans[msgID] = append(d.orphans[msgID], at)
		d.mu.Unlock()
		return
	}
	f.got++
	d.received++
	sentAt := f.sentAt
	d.mu.Unlock()
	d.e2e.add(at.Sub(sentAt))
}

// undelivered returns the number of expected deliveries that never arrived, and a sample of the
// conversations they belonged to. Call once, after the drain period.
func (d *delivery) undelivered() (missing int64, sampleConvs []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := map[string]bool{}
	for _, f := range d.pending {
		if f.got < f.want {
			missing += int64(f.want - f.got)
			if !seen[f.convID] && len(sampleConvs) < 5 {
				seen[f.convID] = true
				sampleConvs = append(sampleConvs, f.convID)
			}
		}
	}
	return missing, sampleConvs
}

func (d *delivery) totals() (expected, received int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.expected, d.received
}

func ms(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0fms", float64(d.Microseconds())/1000)
}
