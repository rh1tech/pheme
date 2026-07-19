package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Provisioning: the accounts, conversations and open streams a run needs before any measurement
// starts. Setup cost is deliberately kept out of the measurement — a run that counted account
// creation would report the admin API's throughput, not the chat path's.

type loadUser struct {
	id    string
	email string
	token string
}

type world struct {
	users  []loadUser
	convs  []conv
	stream *streamPool
	runID  string
}

type conv struct {
	id      string
	members []int // indexes into world.users
}

func (w *world) close() {
	if w.stream != nil {
		w.stream.close()
	}
}

// provision creates the users, logs each of them in, groups them into conversations, and opens
// every SSE stream. It returns once every stream is connected, so the first measured message is not
// racing a half-built world.
func provision(ctx context.Context, c *apiClient, adminToken string, o options) (*world, error) {
	runID := time.Now().UTC().Format("0102-150405")
	fmt.Printf("run         %s\n", runID)
	fmt.Printf("provisioning %d users (%d streams each), %d per conversation...\n",
		o.users, o.streamsPerUser, o.groupSize)

	w := &world{runID: runID, users: make([]loadUser, o.users)}
	start := time.Now()

	// Account creation is rate-limited by the admin API and by bcrypt; 16 at a time keeps setup
	// brisk without turning provisioning itself into the load test.
	if err := parallelFor(ctx, o.users, 16, func(ctx context.Context, i int) error {
		email := fmt.Sprintf("loadtest-%s-%d@%s", runID, i, o.emailDomain)
		u, err := c.createUser(ctx, adminToken, email, o.password)
		if err != nil {
			var apiErr *apiError
			// A repeat run with the same ids finds the accounts already there. That is fine —
			// look up the one that exists rather than failing the run.
			if !errors.As(err, &apiErr) || apiErr.Status != 409 {
				return fmt.Errorf("create %s: %w", email, err)
			}
			if u, err = c.findUser(ctx, adminToken, email); err != nil {
				return fmt.Errorf("resolve existing %s: %w", email, err)
			}
		}
		t, err := c.login(ctx, email, o.password)
		if err != nil {
			return fmt.Errorf("login %s: %w", email, err)
		}
		w.users[i] = loadUser{id: u.ID, email: email, token: t.AccessToken}
		return nil
	}); err != nil {
		return nil, err
	}
	fmt.Printf("  %d accounts ready in %s\n", o.users, time.Since(start).Round(time.Millisecond))

	// Conversations partition the users into disjoint groups. Disjoint matters: overlapping groups
	// would make the per-message recipient count depend on which conversation was picked, and the
	// delivery accounting could no longer tell a drop from a miscount.
	for base := 0; base+o.groupSize <= o.users; base += o.groupSize {
		members := make([]int, 0, o.groupSize)
		ids := make([]string, 0, o.groupSize-1)
		for j := base; j < base+o.groupSize; j++ {
			members = append(members, j)
			if j != base {
				ids = append(ids, w.users[j].id)
			}
		}
		owner := w.users[base]
		cv, err := c.createGroup(ctx, owner.token, fmt.Sprintf("load-%s-%d", runID, base/o.groupSize), ids)
		if err != nil {
			return nil, fmt.Errorf("create conversation: %w", err)
		}
		w.convs = append(w.convs, conv{id: cv.ID, members: members})
	}
	if len(w.convs) == 0 {
		return nil, fmt.Errorf("no conversations: %d users cannot fill a group of %d", o.users, o.groupSize)
	}
	fmt.Printf("  %d conversations of %d\n", len(w.convs), o.groupSize)

	pool, err := openStreams(ctx, c, w, o)
	if err != nil {
		return nil, err
	}
	w.stream = pool
	fmt.Printf("  %d SSE streams connected in %s\n", pool.connected(), time.Since(start).Round(time.Millisecond))
	return w, nil
}

// streamPool holds every open SSE connection and routes what arrives to the run's accounting.
type streamPool struct {
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	opened  *counters
	deliver *delivery

	mu       sync.Mutex
	observed map[string]bool // message ids seen, to notice duplicates
	dupes    int64
	drops    int64 // streams that died mid-run
}

func (p *streamPool) connected() int64 { return p.opened.get("open") }

func (p *streamPool) close() {
	p.cancel()
	// Streams are blocked on a read from a server that will not answer again; give them a moment
	// to notice, but never hang the report on a socket that refuses to close.
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// openStreams connects every user's streams and waits until all of them are live.
func openStreams(ctx context.Context, c *apiClient, w *world, o options) (*streamPool, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	p := &streamPool{
		cancel:   cancel,
		opened:   newCounters(),
		deliver:  newDelivery(),
		observed: map[string]bool{},
	}

	total := len(w.users) * o.streamsPerUser
	for i := range w.users {
		for s := 0; s < o.streamsPerUser; s++ {
			user := w.users[i]
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				err := c.stream(streamCtx, user.token,
					func() { p.opened.inc("open") },
					func(e streamEvent) {
						if e.ChatMessage == nil {
							return
						}
						at := time.Now()
						p.mu.Lock()
						key := e.ChatMessage.ID + "|" + user.id
						if p.observed[key] {
							p.dupes++
							p.mu.Unlock()
							return
						}
						p.observed[key] = true
						p.mu.Unlock()
						p.deliver.arrived(e.ChatMessage.ID, at)
					})
				if err != nil && streamCtx.Err() == nil {
					// A stream that dies while the run is live is a failure, not a shutdown.
					p.mu.Lock()
					p.drops++
					p.mu.Unlock()
					p.opened.inc("stream_error: " + errKind(err))
				}
			}()
		}
	}

	if !waitFor(ctx, 120*time.Second, func() bool { return int(p.connected()) >= total }) {
		got := p.connected()
		cancel()
		return nil, fmt.Errorf("only %d of %d SSE streams connected; the server is refusing "+
			"connections before the test has even started", got, total)
	}
	return p, nil
}

// errKind reduces an error to something countable, so a report groups "connection reset" rather
// than listing ten thousand unique socket errors.
func errKind(err error) string {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("status %d", apiErr.Status)
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection reset"):
		return "connection reset"
	case strings.Contains(msg, "EOF"):
		return "EOF"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "too many open files"):
		return "too many open files (client-side limit — raise ulimit -n)"
	default:
		return msg
	}
}

// parallelFor runs fn for each index with at most `workers` running at once, collecting every
// error rather than stopping at the first.
func parallelFor(ctx context.Context, n, workers int, fn func(context.Context, int) error) error {
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var errs errJoin
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			errs.add(fn(ctx, i))
		}(i)
	}
	wg.Wait()
	return errs.err()
}
