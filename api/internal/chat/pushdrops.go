package chat

import (
	"sync"
	"time"
)

// Reporting notifications that were never sent.
//
// When the push semaphore is full a notification is discarded, and each discard used to write its
// own WARN line. That reads fine in a quiet log and falls apart exactly when it matters: measured
// at the ceiling, one run produced 456 lines in 45 seconds. An operator looking for the reason a
// deploy went wrong is scrolling past hundreds of copies of the same sentence, and any other
// warning in that window is buried under them.
//
// Worse, the volume tells you nothing you can act on. What an operator needs from this is not "here
// is a drop" repeated — it is how many, over how long, which is the number that says whether the
// ceiling is being brushed or sat on.
//
// So: report the first drop immediately, because the transition from working to dropping is the
// event worth seeing, then summarise every reportWindow for as long as it continues.
const reportWindow = 30 * time.Second

// dropCounter accumulates drops and decides when they are worth a log line. Safe for concurrent
// use: drops happen on the request path, from every goroutine serving a send.
type dropCounter struct {
	mu      sync.Mutex
	pending int
	lastLog time.Time
}

// record counts one drop and reports how many have accumulated since the last log line, or false
// if it is not yet time to write one. The count it returns includes the drop just recorded, and
// resets — so summing every reported count yields the true total, with none of them missed and
// none double-counted.
func (d *dropCounter) record(now time.Time) (int, bool) { return d.recordN(1, now) }

// recordN is record for a batch — one fan-out can fail for many devices at once, and counting
// those one at a time would report a rate that is wrong by the size of the conversation.
func (d *dropCounter) recordN(n int, now time.Time) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pending += n
	// A zero lastLog makes the first drop report immediately, which is what makes the onset of
	// dropping visible rather than up to reportWindow late.
	if now.Sub(d.lastLog) < reportWindow {
		return 0, false
	}
	total := d.pending
	d.pending = 0
	d.lastLog = now
	return total, true
}

// The two places that drop notifications: an ordinary message, and a call ringing. They count
// separately because they mean different things — a dropped ring is a call that never visibly
// arrives, which is a good deal worse than a message notification that does not appear.
var (
	messagePushDrops dropCounter
	callPushDrops    dropCounter

	// Notifications that reached a worker too late to be worth sending. A different failure from a
	// full queue — capacity is not short, the backlog is simply draining slower than it fills — so
	// it is counted and reported on its own.
	messageStaleDrops dropCounter

	// Notifications the server tried to send and the push service refused.
	//
	// These were invisible. SendChat returns a Result per device carrying that device's error, and
	// the caller logged only the single error covering the whole call — which is nil when every
	// individual send failed. A push service rejecting every notification for an entire
	// conversation therefore produced silence: no log line, no counter, and a delivery rate that
	// looked perfect from inside the server. It was found by measuring deliveries at the far end
	// and finding fewer than the server believed it had sent.
	pushSendFailures dropCounter
)
