package calls

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// One set of assertions for both mailbox implementations. Production runs the Redis one; the tests
// exercised the in-memory one.
//
// This is the first-to-answer lock. Every device a person is signed in on rings, and exactly one
// must win — if two devices both believe they answered, both open a media session and the call is
// broken for everyone in it. That is not a property a client can enforce for itself, which is why
// it lives here.
//
// The Redis half is skipped unless PHEME_TEST_REDIS_ADDR is set:
//
//	docker run -d --rm -p 6479:6379 redis:7-alpine
//	PHEME_TEST_REDIS_ADDR=localhost:6479 go test ./internal/calls/

func eachMailbox(t *testing.T, fn func(t *testing.T, m Mailbox)) {
	t.Helper()

	t.Run("memory", func(t *testing.T) { fn(t, NewMemory()) })

	addr := os.Getenv("PHEME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Log("PHEME_TEST_REDIS_ADDR not set — skipping the implementation that runs in production")
		return
	}
	t.Run("redis", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: addr})
		if err := client.Ping(context.Background()).Err(); err != nil {
			t.Fatalf("connect to redis: %v", err)
		}
		prefix := "phemecalltest:" + t.Name()
		t.Cleanup(func() {
			keys, _ := client.Keys(context.Background(), prefix+"*").Result()
			if len(keys) > 0 {
				_ = client.Del(context.Background(), keys...).Err()
			}
			// Disconnect, or a client's connection pool outlives the test that made it.
			_ = client.Close()
		})
		fn(t, NewRedis(client, prefix))
	})
}

// Sequence numbers are what let a client say "everything after 4" and get exactly that. They start
// at 1 and never repeat within a call.
func TestMailboxConformance_SequenceNumbersAreMonotonicPerCall(t *testing.T) {
	eachMailbox(t, func(t *testing.T, m Mailbox) {
		ctx := context.Background()

		for want := 1; want <= 3; want++ {
			sig, err := m.Append(ctx, "call-1", []byte(fmt.Sprintf("signal-%d", want)))
			if err != nil {
				t.Fatalf("append: %v", err)
			}
			if sig.Seq != want {
				t.Fatalf("append %d got seq %d", want, sig.Seq)
			}
		}

		// A different call has its own numbering; sharing one would make "since 4" mean different
		// things to two calls happening at once.
		sig, err := m.Append(ctx, "call-2", []byte("first of another call"))
		if err != nil {
			t.Fatalf("append to second call: %v", err)
		}
		if sig.Seq != 1 {
			t.Errorf("a second call started at seq %d, want 1 — sequence numbers are per call", sig.Seq)
		}
	})
}

func TestMailboxConformance_SinceReturnsOnlyWhatFollows(t *testing.T) {
	eachMailbox(t, func(t *testing.T, m Mailbox) {
		ctx := context.Background()
		for i := 1; i <= 4; i++ {
			if _, err := m.Append(ctx, "call", []byte(fmt.Sprintf("s%d", i))); err != nil {
				t.Fatalf("append: %v", err)
			}
		}

		// 0 means "the whole call" — what a device joining late needs.
		all, err := m.Since(ctx, "call", 0)
		if err != nil {
			t.Fatalf("since 0: %v", err)
		}
		if len(all) != 4 {
			t.Fatalf("since 0 returned %d signals, want 4", len(all))
		}
		// Oldest first: signalling only makes sense applied in order.
		for i, sig := range all {
			if sig.Seq != i+1 {
				t.Fatalf("signal %d has seq %d; the order is not oldest-first", i, sig.Seq)
			}
		}

		rest, err := m.Since(ctx, "call", 2)
		if err != nil {
			t.Fatalf("since 2: %v", err)
		}
		if len(rest) != 2 || rest[0].Seq != 3 {
			t.Errorf("since 2 returned %d signals starting at %d, want 2 starting at 3", len(rest), rest[0].Seq)
		}

		// Past the end is empty, not an error: a client that is fully caught up asks this
		// constantly.
		none, err := m.Since(ctx, "call", 4)
		if err != nil {
			t.Fatalf("since 4: %v", err)
		}
		if len(none) != 0 {
			t.Errorf("since 4 returned %d signals, want none", len(none))
		}
	})
}

func TestMailboxConformance_AnUnknownCallIsEmptyNotAnError(t *testing.T) {
	eachMailbox(t, func(t *testing.T, m Mailbox) {
		got, err := m.Since(context.Background(), "never-happened", 0)
		if err != nil {
			t.Fatalf("Since on an unknown call = %v, want no error", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d signals for a call that never happened", len(got))
		}
	})
}

func TestMailboxConformance_TheCiphertextIsReturnedUntouched(t *testing.T) {
	eachMailbox(t, func(t *testing.T, m Mailbox) {
		ctx := context.Background()
		// Bytes the server cannot read and must not mangle — including a NUL and high bytes, which
		// is where a string round trip would corrupt them.
		payload := []byte{0x00, 0xff, 0xfe, 'a', 0x01, 0x80}
		if _, err := m.Append(ctx, "binary", payload); err != nil {
			t.Fatalf("append: %v", err)
		}

		got, err := m.Since(ctx, "binary", 0)
		if err != nil || len(got) != 1 {
			t.Fatalf("since = %d signals, %v", len(got), err)
		}
		if string(got[0].Ciphertext) != string(payload) {
			t.Errorf("ciphertext came back as %v, want %v — the server must pass it through untouched",
				got[0].Ciphertext, payload)
		}
	})
}

// THE POINT OF THE WHOLE TYPE. Every device rings; exactly one answers.
func TestMailboxConformance_ExactlyOneDeviceWinsTheClaim(t *testing.T) {
	eachMailbox(t, func(t *testing.T, m Mailbox) {
		ctx := context.Background()

		winner, isMe, err := m.Claim(ctx, "call", "phone")
		if err != nil {
			t.Fatalf("first claim: %v", err)
		}
		if !isMe || winner != "phone" {
			t.Fatalf("the first device to claim was told it lost: winner=%q isMe=%v", winner, isMe)
		}

		// The loser is told WHO won, immediately and for certain — not left to infer it from a
		// channel that is allowed to drop messages.
		winner, isMe, err = m.Claim(ctx, "call", "laptop")
		if err != nil {
			t.Fatalf("second claim: %v", err)
		}
		if isMe {
			t.Error("two devices both believe they answered the call")
		}
		if winner != "phone" {
			t.Errorf("the loser was told the winner is %q, want phone", winner)
		}

		// The winner re-claiming is still the winner: a retry after a dropped response must not
		// hand the call to somebody else.
		winner, isMe, err = m.Claim(ctx, "call", "phone")
		if err != nil {
			t.Fatalf("re-claim: %v", err)
		}
		if !isMe || winner != "phone" {
			t.Errorf("the winner re-claiming got winner=%q isMe=%v; a retry lost the call", winner, isMe)
		}
	})
}

// Two calls do not share a lock, or answering one would silently answer the other.
func TestMailboxConformance_ClaimsAreScopedToOneCall(t *testing.T) {
	eachMailbox(t, func(t *testing.T, m Mailbox) {
		ctx := context.Background()

		if _, isMe, _ := m.Claim(ctx, "call-a", "phone"); !isMe {
			t.Fatal("first claim on call-a lost")
		}
		winner, isMe, err := m.Claim(ctx, "call-b", "laptop")
		if err != nil {
			t.Fatalf("claim on call-b: %v", err)
		}
		if !isMe || winner != "laptop" {
			t.Errorf("a claim on one call decided another: winner=%q isMe=%v", winner, isMe)
		}
	})
}

// Under a real race — every device claiming at once — exactly one must win. A lock that is merely
// usually right produces two answered calls occasionally, which is worse than one that is always
// wrong, because nobody finds it.
func TestMailboxConformance_ConcurrentClaimsProduceOneWinner(t *testing.T) {
	eachMailbox(t, func(t *testing.T, m Mailbox) {
		ctx := context.Background()
		const devices = 8

		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		winners := map[string]int{}

		for i := 0; i < devices; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				winner, isMe, err := m.Claim(ctx, "race", fmt.Sprintf("device-%d", i))
				if err != nil {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				winners[winner]++
				if isMe {
					wins++
				}
			}(i)
		}
		wg.Wait()

		if wins != 1 {
			t.Errorf("%d of %d devices believe they answered the call, want exactly 1", wins, devices)
		}
		if len(winners) != 1 {
			t.Errorf("the devices disagree about who won: %v", winners)
		}
	})
}
