package chat

import (
	"sync"
	"time"
)

// How much committing a healthy conversation is allowed before somebody should look at it.
//
// Honest churn is small: establishing a group is one Commit, admitting a signed-in device
// or pruning a dead one is one or two more, and each happens occasionally. The July 2026
// incident was five hundred epochs in two days — two clients feeding each other's
// reconcile loops at two Commits a second — and nothing on the server so much as logged a
// line while it happened. This is that line.
const (
	stormAlarmWindow    = 10 * time.Minute
	stormAlarmThreshold = 40
)

// commitStormDetector notices a conversation whose group epoch is advancing at a rate no
// honest membership churn explains. It does not refuse anything — a Commit it dislikes
// might be the very one that repairs the group — it makes the storm impossible to miss.
type commitStormDetector struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	seen      map[string]*stormWindow
}

type stormWindow struct {
	start  time.Time
	n      int
	warned bool
}

func newCommitStormDetector(window time.Duration, threshold int) *commitStormDetector {
	return &commitStormDetector{
		window:    window,
		threshold: threshold,
		seen:      make(map[string]*stormWindow),
	}
}

// Observe records one accepted Commit. It reports how many the conversation has made in
// the current window, and whether this is the Commit that crossed the threshold — true
// exactly once per window, so the caller alarms once per storm rather than once per
// excess Commit.
func (d *commitStormDetector) Observe(conversationID string, now time.Time) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	w := d.seen[conversationID]
	if w == nil || now.Sub(w.start) > d.window {
		// A fresh window — and a cheap sweep of expired ones, so the map's size stays
		// proportional to conversations committing RIGHT NOW rather than ever.
		for id, old := range d.seen {
			if now.Sub(old.start) > d.window {
				delete(d.seen, id)
			}
		}
		w = &stormWindow{start: now}
		d.seen[conversationID] = w
	}

	w.n++
	if w.n >= d.threshold && !w.warned {
		w.warned = true
		return w.n, true
	}
	return w.n, false
}
