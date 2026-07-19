package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// The measured phase: hold a send rate for a fixed duration, then wait for the stragglers and count
// what arrived.

type result struct {
	rate        float64
	elapsed     time.Duration
	sends       *latencies
	e2e         *latencies
	errs        *counters
	sent        int64
	expected    int64
	received    int64
	missing     int64
	missConvs   []string
	dupes       int64
	streamsUp   int64
	streamsLost int64
}

// measure runs one step of the ramp at the given rate.
func measure(ctx context.Context, c *apiClient, w *world, o options, rate float64) *result {
	res := &result{
		rate:  rate,
		sends: &latencies{},
		errs:  newCounters(),
	}
	// Delivery accounting is per step, so one step's drops are not blamed on the next.
	w.stream.deliver = newDelivery()
	res.e2e = w.stream.deliver.e2e

	payload := randomBytes(o.msgBytes)
	interval := time.Duration(float64(time.Second) / rate)
	runCtx, cancel := context.WithTimeout(ctx, o.duration)
	defer cancel()

	// Senders are decoupled from the ticker: a slow server must not slow the offered load, or the
	// test would quietly back off and report a rate the server never actually faced. This is open-
	// loop load, which is what a real user population is — people do not stop typing because the
	// server is busy.
	var wg sync.WaitGroup
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	start := time.Now()
	//nolint:gosec // a load generator's choice of conversation does not need crypto-grade randomness
	pick := rand.New(rand.NewSource(time.Now().UnixNano()))
	var pickMu sync.Mutex

	for {
		select {
		case <-runCtx.Done():
			goto drain
		case <-ticker.C:
			pickMu.Lock()
			cv := w.convs[pick.Intn(len(w.convs))]
			sender := cv.members[pick.Intn(len(cv.members))]
			pickMu.Unlock()

			wg.Add(1)
			go func(cv conv, sender int) {
				defer wg.Done()
				sendOne(ctx, c, w, res, cv, sender, payload, o)
			}(cv, sender)
		}
	}

drain:
	wg.Wait()
	res.elapsed = time.Since(start)
	// Late arrivals are still arrivals. Without this pause a healthy server under load looks like
	// it is dropping messages that were merely still in flight when the clock stopped.
	fmt.Printf("  draining for %s...\n", o.drain)
	select {
	case <-time.After(o.drain):
	case <-ctx.Done():
	}

	res.expected, res.received = w.stream.deliver.totals()
	res.missing, res.missConvs = w.stream.deliver.undelivered()
	w.stream.mu.Lock()
	res.dupes, res.streamsLost = w.stream.dupes, w.stream.drops
	w.stream.mu.Unlock()
	res.streamsUp = w.stream.connected()
	return res
}

// sendOne posts a single message and records what it cost.
func sendOne(ctx context.Context, c *apiClient, w *world, res *result, cv conv, sender int, payload []byte, o options) {
	// EVERY member sees it, once per stream — including the sender's own streams. The server
	// echoes a message back to its author, which is what lets a second device of the sender's stay
	// in sync. This was measured, not assumed: the first run of this test expected members-1 and
	// reported 125% delivery, which is the accounting catching the wrong expectation rather than a
	// server fault.
	want := len(cv.members) * o.streamsPerUser

	begin := time.Now()
	msg, err := c.postMessage(ctx, w.users[sender].token, cv.id, payload)
	took := time.Since(begin)

	if err != nil {
		res.errs.inc("send: " + errKind(err))
		return
	}
	res.sends.add(took)
	res.errs.inc("send: ok")
	// Recorded with the time the request STARTED, not when it returned: end-to-end latency is what
	// the sender waited from pressing send, which includes the server's own processing.
	w.stream.deliver.sent(msg.ID, cv.id, want, begin)
}

// brokenDown reports whether this step shows the server past its limit — the condition for stopping
// a ramp. Any of three things counts, because each is independently fatal to the experience:
// messages that never arrive, sends that error, or a p99 no user would tolerate.
func (r *result) brokenDown() bool {
	if r.expected > 0 && float64(r.missing)/float64(r.expected) > 0.01 {
		return true
	}
	if r.sendErrors() > 0 && float64(r.sendErrors())/float64(r.sendErrors()+r.okSends()) > 0.01 {
		return true
	}
	_, _, p99, _, n := r.sends.summary()
	return n > 0 && p99 > 2*time.Second
}

func (r *result) okSends() int64 { return r.errs.get("send: ok") }

func (r *result) sendErrors() int64 {
	var n int64
	for k, v := range r.errs.snapshot() {
		if k != "send: ok" && len(k) > 5 && k[:5] == "send:" {
			n += v
		}
	}
	return n
}

func (r *result) report(o options, rate float64) {
	ok := r.okSends()
	achieved := float64(ok) / r.elapsed.Seconds()

	fmt.Printf("\n  offered %.0f msg/s, achieved %.1f msg/s over %s\n", rate, achieved, r.elapsed.Round(time.Millisecond))
	fmt.Printf("  streams %d open, %d lost mid-run\n", r.streamsUp, r.streamsLost)

	p50, p95, p99, max, n := r.sends.summary()
	fmt.Printf("  send latency      n=%-7d p50=%-8s p95=%-8s p99=%-8s max=%s\n", n, ms(p50), ms(p95), ms(p99), ms(max))
	ep50, ep95, ep99, emax, en := r.e2e.summary()
	fmt.Printf("  send→SSE latency  n=%-7d p50=%-8s p95=%-8s p99=%-8s max=%s\n", en, ms(ep50), ms(ep95), ms(ep99), ms(emax))

	if r.expected > 0 {
		pct := 100 * float64(r.received) / float64(r.expected)
		fmt.Printf("  delivery          %d/%d (%.2f%%)", r.received, r.expected, pct)
		if r.missing > 0 {
			fmt.Printf("  ** %d NEVER ARRIVED **", r.missing)
		}
		fmt.Println()
	}
	if r.dupes > 0 {
		fmt.Printf("  duplicates        %d (the same message delivered twice to one user)\n", r.dupes)
	}

	if errs := r.errorLines(); len(errs) > 0 {
		fmt.Println("  errors:")
		for _, line := range errs {
			fmt.Printf("    %s\n", line)
		}
	}

	// The interpretation, so a run is a conclusion rather than a wall of numbers.
	switch {
	case r.missing > 0:
		fmt.Printf("\n  VERDICT: messages were lost. %d of %d deliveries never reached a stream, and\n"+
			"  nothing in the HTTP responses said so — every send returned 201. This is the live\n"+
			"  bus dropping events once a subscriber's buffer fills.\n", r.missing, r.expected)
		if len(r.missConvs) > 0 {
			fmt.Printf("  affected conversations included: %v\n", r.missConvs)
		}
	case r.sendErrors() > 0:
		fmt.Printf("\n  VERDICT: %d sends failed outright at %.0f msg/s.\n", r.sendErrors(), rate)
	case p99 > 2*time.Second:
		fmt.Printf("\n  VERDICT: sends still succeed, but p99 is %s. This is past usable.\n", ms(p99))
	case p99 > 500*time.Millisecond:
		fmt.Printf("\n  VERDICT: holding, but p99 of %s is where users start noticing.\n", ms(p99))
	default:
		fmt.Printf("\n  VERDICT: comfortable. Nothing lost, p99 %s.\n", ms(p99))
	}
}

func (r *result) errorLines() []string {
	var out []string
	for k, v := range r.errs.snapshot() {
		if k == "send: ok" {
			continue
		}
		out = append(out, fmt.Sprintf("%-40s %d", k, v))
	}
	return out
}
