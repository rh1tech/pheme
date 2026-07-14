package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/live"
)

// sseEvent is the decoded payload of a single "event: message" SSE frame.
type sseEvent struct {
	ChannelID string         `json:"channelId"`
	Message   domain.Message `json:"message"`
}

// streamConn opens the SSE endpoint against a live test server and reads frames.
type streamConn struct {
	cancel context.CancelFunc
	body   interface{ Close() error }
	sc     *bufio.Scanner
}

// openStream connects to GET /v1/stream and blocks until the server's
// ": connected" preamble arrives, guaranteeing the subscription is registered
// before the caller publishes anything (so no event can be missed by a race).
func openStream(t *testing.T, baseURL, token string) *streamConn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/stream?token="+token, nil)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("stream status = %d, want 200", res.StatusCode)
	}
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), ": connected") {
			return &streamConn{cancel: cancel, body: res.Body, sc: sc}
		}
	}
	cancel()
	t.Fatalf("never received SSE connected preamble")
	return nil
}

// next reads the next "event: message" frame and returns its decoded payload.
// The read is bounded by a watchdog that cancels the connection, so a missing
// event fails fast instead of hanging the suite.
func (c *streamConn) next(t *testing.T) sseEvent {
	t.Helper()
	timer := time.AfterFunc(2*time.Second, c.cancel)
	defer timer.Stop()
	for c.sc.Scan() {
		line := c.sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev sseEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("decode sse data: %v", err)
		}
		return ev
	}
	t.Fatalf("stream closed before next event arrived")
	return sseEvent{}
}

func (c *streamConn) close() {
	c.cancel()
	_ = c.body.Close()
}

// publish emits a live event and returns the seeded message so the test can
// assert on its identity.
func (f *appFixture) publish(t *testing.T, bus live.Bus, channelID, body string) domain.Message {
	t.Helper()
	msg := f.seedMessage(t, channelID, "live", body)
	bus.Publish(live.Event{ChannelID: channelID, Message: msg})
	return msg
}

// TestBlockedUserLosesLiveStreamImmediately verifies that blocking a user with
// an open SSE session takes effect on the very next event, without a reconnect.
//
// Determinism without timeouts: the user is an active member of two channels (A
// and B). After being blocked from A, the test publishes an A event (must be
// dropped) immediately followed by a B event (must be delivered). SSE frames on
// one connection are ordered and Publish fans out synchronously in call order,
// so if the next frame the user receives is the B event, the A event was
// correctly skipped post-block.
func TestBlockedUserLosesLiveStreamImmediately(t *testing.T) {
	f := newAppFixture(t)
	bus := live.NewMemoryBus()
	f.h.Live = bus // route the stream handler through a bus the test can publish to

	srv := httptest.NewServer(f.mux)
	defer srv.Close()

	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, subUser := f.tokenFor(t, "sub@b.com")

	chA := f.createChannelMode(t, owner, "A", domain.ModeOpen)
	chB := f.createChannelMode(t, owner, "B", domain.ModeOpen)
	for _, ch := range []domain.Channel{chA, chB} {
		if rec := f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID}); rec.Code != http.StatusCreated {
			t.Fatalf("join %s: %d %s", ch.Name, rec.Code, rec.Body)
		}
	}

	conn := openStream(t, srv.URL, subTok)
	defer conn.close()

	// While active, the member receives channel A's live events.
	a1 := f.publish(t, bus, chA.ID, "before-block")
	if ev := conn.next(t); ev.ChannelID != chA.ID || ev.Message.ID != a1.ID {
		t.Fatalf("pre-block event = (ch %s, msg %s), want (ch %s, msg %s)", ev.ChannelID, ev.Message.ID, chA.ID, a1.ID)
	}

	// Admin blocks the user from channel A.
	if rec := f.do(http.MethodPatch, "/v1/channels/"+chA.ID+"/members/"+subUser.ID, owner,
		map[string]any{"status": "blocked"}); rec.Code != http.StatusOK {
		t.Fatalf("block: %d %s", rec.Code, rec.Body)
	}

	// Next, an A event (must now be dropped) followed by a B event (still allowed).
	a2 := f.publish(t, bus, chA.ID, "after-block")
	b1 := f.publish(t, bus, chB.ID, "still-allowed")

	ev := conn.next(t)
	if ev.ChannelID == chA.ID || ev.Message.ID == a2.ID {
		t.Fatalf("blocked user still received channel A event %s after block; want it dropped", a2.ID)
	}
	if ev.ChannelID != chB.ID || ev.Message.ID != b1.ID {
		t.Fatalf("next event = (ch %s, msg %s), want channel B event (ch %s, msg %s)", ev.ChannelID, ev.Message.ID, chB.ID, b1.ID)
	}
}

// A live stream must not outlive the token that opened it.
//
// The token is checked once, at connect, because EventSource cannot send an Authorization
// header and the credential has to ride in the URL. A stream that then ran forever would be a
// session that never ends — one that survives the access token expiring, and that a sign-out
// on another device could not reach.
//
// The client's side of this is the interesting half: EventSource reconnects on a dropped
// connection but NOT on an HTTP error status, and it always retries the same URL — so a client
// that let the browser handle reconnection would replay a dead token, get a 401, and go silent
// permanently. It reopens with a fresh token instead (see useEventStream).
func TestStreamEndsWhenItsTokenExpires(t *testing.T) {
	f := newAppFixture(t)
	// A token that outlives the connect handshake and little else.
	shortLived := auth.NewTokenManager("test-secret", 700*time.Millisecond, 24*time.Hour)
	f.h.Tokens = shortLived

	u := seedUser(t, f.store, "expiring-stream@pheme.test", domain.RoleUser)
	token, _, err := shortLived.Issue(u.ID, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	srv := httptest.NewServer(f.mux)
	defer srv.Close()

	conn := openStream(t, srv.URL, token)
	defer conn.close()

	// The stream is open and idle. It must end on its own when the token behind it expires,
	// rather than keep an authenticated channel alive on a credential that is no longer valid.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for conn.sc.Scan() { //nolint:revive // draining until the server hangs up is the assertion
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its access token; it must end so the client reconnects with a fresh one")
	}
}
