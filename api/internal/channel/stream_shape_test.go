package channel

import (
	"strings"
	"testing"
	"time"
)

// The SSE stream used to emit a ~9-byte comment every 25.000 seconds and tear
// itself down on the token's expiry to the millisecond. Packet timing and size
// survive encryption, so that pair was a recognisable signature for Pheme
// traffic without decrypting any of it. These tests pin the properties that
// removed it.

func TestHeartbeatIntervalStaysInsideItsBounds(t *testing.T) {
	for range 1000 {
		got := nextHeartbeat()
		if got < streamHeartbeatMin || got >= streamHeartbeatMax {
			t.Fatalf("nextHeartbeat() = %v, want within [%v, %v)",
				got, streamHeartbeatMin, streamHeartbeatMax)
		}
	}
}

// The ceiling matters as much as the randomness: intermediaries (nginx,
// Cloudflare, carrier NAT) drop a silent connection after their own timeout,
// which is commonly 60s. A heartbeat that could exceed that would trade a
// fingerprint for dropped streams.
func TestHeartbeatCeilingStaysUnderTypicalIdleTimeouts(t *testing.T) {
	if streamHeartbeatMax > 60*time.Second {
		t.Errorf("streamHeartbeatMax = %v, want <= 60s so idle streams are not reaped",
			streamHeartbeatMax)
	}
}

func TestHeartbeatIntervalActuallyVaries(t *testing.T) {
	seen := make(map[time.Duration]struct{})
	for range 100 {
		seen[nextHeartbeat()] = struct{}{}
	}
	// A fixed interval would collapse to one value. Anything above a handful
	// proves the draw is live; the exact count is not the point.
	if len(seen) < 10 {
		t.Errorf("saw %d distinct intervals across 100 draws, want a spread", len(seen))
	}
}

func TestHeartbeatCommentIsAnIgnorableSSECommentOfVaryingLength(t *testing.T) {
	lengths := make(map[int]struct{})
	for range 200 {
		got := heartbeatComment()
		// Both consumers skip a line starting with ':' — the browser's EventSource
		// by spec, the mobile client's parser by an explicit check. If this ever
		// stopped being a comment it would be delivered as an event instead.
		if !strings.HasPrefix(got, ":") {
			t.Fatalf("heartbeatComment() = %q, want an SSE comment line", got)
		}
		// A newline would end the comment early and turn the padding into a
		// second line the client would try to parse.
		if strings.ContainsAny(got, "\r\n") {
			t.Fatalf("heartbeatComment() = %q, must stay on one line", got)
		}
		lengths[len(got)] = struct{}{}
	}
	if len(lengths) < 10 {
		t.Errorf("saw %d distinct comment lengths across 200 draws, want a spread", len(lengths))
	}
}

// The teardown jitter is only ever subtracted. The token is checked once, at
// connect, so a stream that outlived it would be a session that never ends —
// a signed-out user would keep receiving events until they closed the tab.
func TestExpiryJitterOnlyEverPullsTheTeardownEarlier(t *testing.T) {
	cases := []struct {
		name      string
		remaining time.Duration
	}{
		{"typical 15m access token", 15 * time.Minute},
		{"short-TTL deployment", 30 * time.Second},
		{"token nearly expired", 2 * time.Second},
		{"token already expired", -1 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for range 500 {
				got := jitteredExpiry(tc.remaining)
				if got > tc.remaining {
					t.Fatalf("jitteredExpiry(%v) = %v, must never extend past the token",
						tc.remaining, got)
				}
			}
		})
	}
}

// Scaling to a fifth of the remaining lifetime keeps a short-TTL deployment from
// having its streams torn down immediately on connect.
func TestExpiryJitterLeavesMostOfAShortTokenUsable(t *testing.T) {
	const remaining = 30 * time.Second
	for range 500 {
		if got := jitteredExpiry(remaining); got < remaining*4/5 {
			t.Fatalf("jitteredExpiry(%v) = %v, want at least 80%% of the lifetime kept",
				remaining, got)
		}
	}
}

func TestExpiryJitterActuallyVariesForALongToken(t *testing.T) {
	seen := make(map[time.Duration]struct{})
	for range 100 {
		seen[jitteredExpiry(15*time.Minute)] = struct{}{}
	}
	if len(seen) < 10 {
		t.Errorf("saw %d distinct teardowns across 100 draws, want a spread", len(seen))
	}
}
