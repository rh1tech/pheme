// Package calls holds the short-lived state behind a 1:1 voice call: the signalling
// mailbox, and the lock that decides which of a person's devices answered first.
//
// Nothing here is durable and nothing here is readable. A call's signals are opaque
// ciphertext (encrypted under a key derived from the conversation's MLS group, which the
// server does not have), they live for two minutes, and then they are gone. No call is
// ever written to the database.
//
// The mailbox exists because the live event bus is allowed to drop events: it hands each
// subscriber a small buffer and discards anything that will not fit. For chat that is a
// message you can scroll back to. For a call it is a dropped SDP answer, which is a call
// that silently never connects. So SSE carries only a NUDGE — "call X has a signal N" —
// and the client fetches the actual signal from here, where it is ordered and cannot be
// lost. That also recovers anything missed while the browser's EventSource was reconnecting,
// which the bus has no answer for at all.
package calls

import (
	"context"
	"sync"
	"time"
)

// How long a call's signals and its answer lock survive. Long enough to place a call and
// have it answered; short enough that nothing here is really state.
const TTL = 2 * time.Minute

// Signal is one opaque, encrypted signalling blob, with the position that lets a client ask
// for everything it has not seen.
type Signal struct {
	Seq        int    `json:"seq"`
	Ciphertext []byte `json:"ciphertext"`
}

// Mailbox is the per-call signalling channel and the answer lock.
type Mailbox interface {
	// Append stores a signal and returns its sequence number: 1 for the first signal of a
	// call, then 2, 3… Sequence numbers are per call and monotonic, which is what lets a
	// client say "give me everything after 4" and get exactly that.
	Append(ctx context.Context, callID string, ciphertext []byte) (Signal, error)

	// Since returns the signals after seq, oldest first. Passing 0 returns the whole call.
	Since(ctx context.Context, callID string, seq int) ([]Signal, error)

	// Claim is the first-to-answer lock.
	//
	// Every device a person is signed in on rings, and exactly one of them must win. It
	// returns the winning device and whether the caller IS that device — so the loser learns
	// it lost, immediately and for certain, rather than being told over a channel that is
	// permitted to drop the message.
	//
	// That distinction is the whole reason this exists. A pure client-side race would leave
	// a losing device ringing with its microphone already open until some timeout fired,
	// because the "someone else answered" broadcast rides the same lossy bus as everything
	// else. A live microphone is not something to arbitrate over a channel that may silently
	// drop the message.
	Claim(ctx context.Context, callID, deviceID string) (winner string, won bool, err error)
}

// Memory is an in-process Mailbox, for development and tests. Production uses Redis, so
// that two App API instances agree about who answered.
type Memory struct {
	mu    sync.Mutex
	calls map[string]*memCall
}

type memCall struct {
	signals []Signal
	winner  string
	expires time.Time
}

func NewMemory() *Memory {
	return &Memory{calls: make(map[string]*memCall)}
}

func (m *Memory) Append(_ context.Context, callID string, ciphertext []byte) (Signal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.liveCall(callID)
	s := Signal{Seq: len(c.signals) + 1, Ciphertext: ciphertext}
	c.signals = append(c.signals, s)
	return s, nil
}

func (m *Memory) Since(_ context.Context, callID string, seq int) ([]Signal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.calls[callID]
	if !ok || time.Now().After(c.expires) {
		return nil, nil
	}
	if seq < 0 {
		seq = 0
	}
	if seq >= len(c.signals) {
		return nil, nil
	}
	// Sequence numbers are 1-based, so "after seq" starts at index seq.
	out := make([]Signal, len(c.signals)-seq)
	copy(out, c.signals[seq:])
	return out, nil
}

func (m *Memory) Claim(_ context.Context, callID, deviceID string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.liveCall(callID)
	if c.winner == "" {
		c.winner = deviceID
		return deviceID, true, nil
	}
	// Claiming twice from the SAME device is not losing — a client that retries after a
	// dropped response must not be told it lost a race against itself.
	return c.winner, c.winner == deviceID, nil
}

// liveCall returns the call, expiring and replacing it if its time is up. Callers hold the
// lock. Expiry is lazy: there is no sweeper, because a call that nobody touches again is a
// few hundred bytes that the next call with the same id would replace anyway.
func (m *Memory) liveCall(callID string) *memCall {
	c, ok := m.calls[callID]
	if !ok || time.Now().After(c.expires) {
		c = &memCall{expires: time.Now().Add(TTL)}
		m.calls[callID] = c
	}
	return c
}
