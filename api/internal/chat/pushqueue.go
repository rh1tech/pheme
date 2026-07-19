package chat

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Absorbing bursts of notifications instead of discarding them.
//
// Sending used to take one of 64 process-wide slots and give up if none was free. That bounded the
// work, which it had to, but it made the failure abrupt and total: measured, notifications began
// disappearing at about 40 messages per second in ten-person conversations, and the unit of loss was
// the MESSAGE — one unavailable slot silenced every recipient of that message at once, while the
// sender saw 201 Created.
//
// A notification that arrives a little late is worth far more than one that never arrives, so the
// slots become a worker pool with a queue in front. A burst above what the workers can push waits
// instead of vanishing. Sustained load above what they can push still cannot be absorbed by any
// queue — that is arithmetic, not policy — so the queue is bounded and a full one still drops. The
// difference is where that point sits and how gradually it is approached.
//
// Three bounds, because a queue with only one of them fails in a way the others would have caught:
//
//   - depth, so the backlog cannot grow without limit;
//   - BYTES, because a queued job holds the message ciphertext for the preview payload, and depth
//     alone would let a few thousand large messages hold gigabytes;
//   - age, because a notification for a message the recipient read ten minutes ago is noise, and
//     without this bound a deep backlog turns into unbounded delivery latency rather than dropping.
const (
	// Concurrent fan-outs. Each may talk to webPushConcurrency (16) push services at once, so this
	// also sets the ceiling on outbound sockets: 64 x 16 = 1024. Raising it raises that too, which
	// is the number to check against file descriptor limits and any provider's rate limit — not
	// this constant on its own.
	pushWorkers = 64

	// Queued fan-outs. At 18 notifications each this is a burst of roughly 74000 notifications
	// absorbed before anything is dropped.
	pushBacklog = 4096

	// Ciphertext held by the queue. The real memory bound: 4096 large messages would be gigabytes,
	// and message size is attacker-influenced.
	pushQueueBytes = 64 << 20

	// How late a notification may be and still be worth sending. Past this the recipient has very
	// likely seen the message, and delivering anyway spends capacity that newer messages need.
	pushMaxWait = 60 * time.Second
)

// pushJob is one message's fan-out, waiting its turn.
type pushJob struct {
	h        *Handler
	convID   string
	senderID string
	msg      domain.ChatMessage
	members  []domain.ConversationMember
	queuedAt time.Time
}

func (j pushJob) size() int64 { return int64(len(j.msg.Ciphertext)) }

// pushPump is the queue and its workers. It is a type rather than a set of package variables so a
// test can run one with its own bounds and its own clock, without a live handler behind it.
type pushPump struct {
	q        chan pushJob
	queued   atomic.Int64 // bytes of ciphertext currently queued
	maxBytes int64
	maxWait  time.Duration

	now   func() time.Time
	run   func(pushJob) // what a worker does with a job; the real one fans out
	start sync.Once

	// Counted so tests and operators can tell the two kinds of loss apart: a full queue means not
	// enough capacity, a stale job means the backlog is draining slower than it fills.
	full  atomic.Int64
	stale atomic.Int64
}

func newPushPump(depth int, maxBytes int64, maxWait time.Duration, run func(pushJob)) *pushPump {
	return &pushPump{
		q: make(chan pushJob, depth), maxBytes: maxBytes, maxWait: maxWait,
		now: time.Now, run: run,
	}
}

// offer queues a job, or reports false if it cannot be queued at all.
//
// It never blocks. This is called on the request path, and making a send wait for a saturated push
// provider would turn a notification problem into a messaging problem — which is the one thing the
// original drop-on-full design got unambiguously right, and is kept.
func (p *pushPump) offer(job pushJob) bool {
	p.startWorkers()

	// Reserve the bytes before taking the slot, and give them back if the slot is not there. Under
	// concurrent offers the reserved total can briefly overshoot maxBytes by the size of the
	// messages racing here; that is bounded by the number of concurrent senders and is the reason
	// the limit is a budget rather than a hard allocation.
	size := job.size()
	if p.queued.Add(size) > p.maxBytes {
		p.queued.Add(-size)
		p.full.Add(1)
		return false
	}
	select {
	case p.q <- job:
		return true
	default:
		p.queued.Add(-size)
		p.full.Add(1)
		return false
	}
}

func (p *pushPump) startWorkers() {
	p.start.Do(func() {
		for i := 0; i < pushWorkers; i++ {
			go p.worker()
		}
	})
}

func (p *pushPump) worker() {
	for job := range p.q {
		// Release the reservation on dequeue, not after the fan-out: the ciphertext stops being
		// queued the moment it is picked up, and holding the budget for the whole send would make
		// the queue shallower than it is configured to be.
		p.queued.Add(-job.size())

		if waited := p.now().Sub(job.queuedAt); waited > p.maxWait {
			p.stale.Add(1)
			// Reported separately from a full queue, because they call for different responses: a
			// full queue means capacity is short, while stale jobs mean the backlog is draining
			// slower than it fills and delivery is running late. Counting one without saying it out
			// loud would leave the slower, quieter failure invisible.
			if job.h != nil {
				if n, report := messageStaleDrops.record(p.now()); report {
					job.h.logger().Warn("chat push: notifications too old to send, dropped",
						"dropped", n, "over", reportWindow, "waited", waited.Round(time.Second),
						"maxWait", p.maxWait)
				}
			}
			continue
		}
		p.run(job)
	}
}

// depth reports how many jobs are waiting. For tests and for anything that later wants to expose it.
func (p *pushPump) depth() int { return len(p.q) }

// The process-wide pump. Its worker function is the real fan-out.
var messagePush = newPushPump(pushBacklog, pushQueueBytes, pushMaxWait, func(j pushJob) {
	j.h.deliverPush(j.convID, j.senderID, j.msg, j.members)
})

// Ringing has its own, much smaller, budget — and keeps the old drop-on-full behaviour.
//
// A late message notification is still useful; a late ring is not, because the call it belongs to
// has already stopped ringing. Queueing one would deliver a notification about a call that no
// longer exists, so past this ceiling a ring is dropped, exactly as before.
//
// Separating it from the message pool is a fix in its own right. Both used to draw on the same 64
// slots, so a burst of messages could consume all of them and leave calls unable to ring at all —
// the load that matters least starving the notification that matters most.
const maxConcurrentRings = 32

var ringSlots = make(chan struct{}, maxConcurrentRings)
