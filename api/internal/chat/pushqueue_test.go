package chat

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// A pump whose workers record what they were given instead of pushing anything.
func testPump(t *testing.T, depth int, maxBytes int64, maxWait time.Duration) (*pushPump, *ranJobs) {
	t.Helper()
	ran := &ranJobs{done: make(chan struct{}, 4096)}
	p := newPushPump(depth, maxBytes, maxWait, ran.record)
	return p, ran
}

type ranJobs struct {
	mu   sync.Mutex
	jobs []pushJob
	done chan struct{}
}

func (r *ranJobs) record(j pushJob) {
	r.mu.Lock()
	r.jobs = append(r.jobs, j)
	r.mu.Unlock()
	r.done <- struct{}{}
}

func (r *ranJobs) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

// waitFor blocks until n jobs have run, or fails — never a bare sleep, which would make this
// test's result depend on how loaded the machine is.
func (r *ranJobs) waitFor(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d jobs ran", r.count(), n)
		}
	}
}

func job(size int, at time.Time) pushJob {
	return pushJob{
		convID: "c1", senderID: "u1", queuedAt: at,
		msg: domain.ChatMessage{Ciphertext: make([]byte, size)},
	}
}

// The whole point of the change: a burst larger than the worker count is delivered rather than
// discarded. Under the old semaphore anything past 64 in flight was lost outright.
func TestPushPump_AbsorbsABurstLargerThanTheWorkerPool(t *testing.T) {
	p, ran := testPump(t, 1024, pushQueueBytes, time.Minute)

	const burst = pushWorkers * 8
	for i := 0; i < burst; i++ {
		if !p.offer(job(16, time.Now())) {
			t.Fatalf("job %d of %d was refused; a burst this size is exactly what the queue exists "+
				"to absorb", i, burst)
		}
	}
	ran.waitFor(t, burst)

	if got := ran.count(); got != burst {
		t.Errorf("%d of %d queued notifications were delivered", got, burst)
	}
}

// Depth is still bounded — the queue absorbs bursts, it does not promise to absorb everything.
// Sustained load above what the workers can push cannot be absorbed by any queue, and pretending
// otherwise is how a process runs out of memory instead of dropping a notification.
func TestPushPump_RefusesBeyondItsDepth(t *testing.T) {
	// Workers would drain a running pump, so this one blocks its single worker slot forever and
	// measures the queue itself.
	block := make(chan struct{})
	p := newPushPump(4, pushQueueBytes, time.Minute, func(pushJob) { <-block })
	defer close(block)

	accepted := 0
	for i := 0; i < 4+pushWorkers+50; i++ {
		if p.offer(job(16, time.Now())) {
			accepted++
		}
	}

	// Every worker takes one job and blocks; the queue holds its depth; the rest are refused.
	if want := 4 + pushWorkers; accepted != want {
		t.Errorf("accepted %d jobs, want %d (%d queued + %d workers each holding one)",
			accepted, want, 4, pushWorkers)
	}
	if p.full.Load() != 50 {
		t.Errorf("%d refusals were counted, want 50", p.full.Load())
	}
}

// The bound that depth alone would miss. A queued job holds the message ciphertext for the preview
// payload, so a few thousand large messages is gigabytes — and message size is influenced by
// whoever is sending.
func TestPushPump_RefusesBeyondItsByteBudget(t *testing.T) {
	block := make(chan struct{})
	const budget = 1 << 20
	// Depth is generous, so only the byte budget can stop this.
	p := newPushPump(1024, budget, time.Minute, func(pushJob) { <-block })
	defer close(block)

	// Fill the workers first with tiny jobs so they are not what limits this.
	for i := 0; i < pushWorkers; i++ {
		p.offer(job(1, time.Now()))
	}

	const each = 64 * 1024
	accepted := 0
	for i := 0; i < 100; i++ {
		if p.offer(job(each, time.Now())) {
			accepted++
		}
	}

	if accepted > budget/each {
		t.Errorf("queued %d messages of %d bytes against a %d-byte budget — %d bytes held, which "+
			"is the memory bound the depth limit cannot express",
			accepted, each, budget, accepted*each)
	}
	if accepted == 0 {
		t.Error("the byte budget refused everything; it is meant to bound the queue, not disable it")
	}
}

// Age is the third bound. Without it a deep backlog becomes unbounded delivery latency: the queue
// stays full, every job eventually gets sent, and recipients are buzzed about messages they read
// ten minutes ago.
func TestPushPump_DropsJobsThatWaitedTooLong(t *testing.T) {
	p, ran := testPump(t, 64, pushQueueBytes, 30*time.Second)

	// A clock the test controls, so this does not depend on real elapsed time.
	base := time.Now()
	p.now = func() time.Time { return base.Add(time.Hour) }

	if !p.offer(job(16, base)) {
		t.Fatal("setup: the job was not queued")
	}

	// It must be dropped, not delivered.
	select {
	case <-ran.done:
		t.Error("a notification queued an hour ago was delivered; the recipient has long since " +
			"seen the message, and sending it spends capacity newer messages need")
	case <-time.After(500 * time.Millisecond):
	}
	if p.stale.Load() != 1 {
		t.Errorf("%d stale drops counted, want 1 — the two kinds of loss must be distinguishable, "+
			"since a full queue and a slow-draining one need different responses", p.stale.Load())
	}
}

// A job that is delivered must not leave its bytes reserved. Leaking the reservation would shrink
// the queue on every message until it refused everything — a slow strangulation that only shows up
// after hours of traffic, which is the worst kind of bug to find in production.
func TestPushPump_ReleasesTheByteBudgetAfterDelivery(t *testing.T) {
	p, ran := testPump(t, 256, pushQueueBytes, time.Minute)

	const n = 200
	for i := 0; i < n; i++ {
		if !p.offer(job(4096, time.Now())) {
			t.Fatalf("job %d was refused", i)
		}
	}
	ran.waitFor(t, n)

	// Drain settles asynchronously; give the last decrement a moment to land.
	deadline := time.Now().Add(2 * time.Second)
	for p.queued.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if held := p.queued.Load(); held != 0 {
		t.Errorf("%d bytes are still counted as queued after everything was delivered; the queue "+
			"shrinks on every message until it refuses all of them", held)
	}
}

// offer runs on the request path. If it ever blocks, a saturated push provider stops being a
// notification problem and becomes a messaging problem — which is the one thing the original
// drop-on-full design got right and this change must keep.
func TestPushPump_OfferNeverBlocksTheCaller(t *testing.T) {
	block := make(chan struct{})
	p := newPushPump(2, pushQueueBytes, time.Minute, func(pushJob) { <-block })
	defer close(block)

	// Saturate workers and queue, so every further offer must take the refusal path.
	for i := 0; i < pushWorkers+2; i++ {
		p.offer(job(16, time.Now()))
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			p.offer(job(16, time.Now()))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("offer blocked against a full queue; sending a message now waits on the push provider")
	}
}

// Senders are concurrent by nature. Every offer must be accounted for as either queued or refused,
// with no job silently lost between the two — run under -race.
func TestPushPump_AccountsForEveryOfferUnderConcurrency(t *testing.T) {
	p, ran := testPump(t, 512, pushQueueBytes, time.Minute)

	const senders, each = 32, 100
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if p.offer(job(64, time.Now())) {
					accepted.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	acc := accepted.Load()
	ran.waitFor(t, int(acc))

	if refused := p.full.Load(); acc+refused != senders*each {
		t.Errorf("%d accepted + %d refused = %d, want %d offers accounted for",
			acc, refused, acc+refused, senders*each)
	}
	if got := int64(ran.count()); got != acc {
		t.Errorf("%d jobs were accepted but %d ran; the difference vanished silently", acc, got)
	}
}
