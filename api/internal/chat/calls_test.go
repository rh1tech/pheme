package chat

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rh1tech/pheme/api/internal/calls"
	"github.com/rh1tech/pheme/api/internal/push"
)

func (f *fixture) enableCalls() { f.handler.Mailbox = calls.NewMemory() }

type signalResponse struct {
	Seq        int    `json:"seq"`
	Ciphertext []byte `json:"ciphertext"`
}

func postSignal(t *testing.T, f *fixture, token, conv, callID string, body []byte, ring bool) (int, signalResponse) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/"+callID+"/signal", token, map[string]any{
		"ciphertext": body,
		"ring":       ring,
	})
	var out signalResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func getSignals(t *testing.T, f *fixture, token, conv, callID string, since int) []signalResponse {
	t.Helper()
	rec := f.do(http.MethodGet,
		"/v1/conversations/"+conv+"/calls/"+callID+"/signals?since="+strconv.Itoa(since), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get signals: got %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Signals []signalResponse `json:"signals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode signals: %v", err)
	}
	return out.Signals
}

// The signalling channel must be ordered and lossless, because a dropped SDP answer is a
// call that silently never connects. The live bus cannot promise that — it drops events for
// a slow subscriber — so the signals live in a mailbox the client can re-read from a known
// position, and the bus only ever carries a nudge saying to come and look.
func TestCallSignalsAreOrderedAndReplayable(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	_, aliceToken := f.user(t, "alice-call@pheme.test")
	bobID, bobToken := f.user(t, "bob-call@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	for i, blob := range [][]byte{[]byte("offer"), []byte("ice-1"), []byte("ice-2")} {
		code, got := postSignal(t, f, aliceToken, conv, "call-1", blob, i == 0)
		if code != http.StatusOK {
			t.Fatalf("signal %d: got %d", i, code)
		}
		if got.Seq != i+1 {
			t.Fatalf("signal %d: seq = %d, want %d — sequence must be monotonic per call", i, got.Seq, i+1)
		}
	}

	// Bob, who has seen nothing, asks for everything.
	all := getSignals(t, f, bobToken, conv, "call-1", 0)
	if len(all) != 3 {
		t.Fatalf("got %d signals, want 3", len(all))
	}
	if string(all[0].Ciphertext) != "offer" || string(all[2].Ciphertext) != "ice-2" {
		t.Fatalf("signals came back out of order: %+v", all)
	}

	// And a device that already saw the first two — because its live stream was up — asks
	// only for what it missed. This is what makes a dropped nudge cost milliseconds and not
	// the call.
	rest := getSignals(t, f, bobToken, conv, "call-1", 2)
	if len(rest) != 1 || string(rest[0].Ciphertext) != "ice-2" || rest[0].Seq != 3 {
		t.Fatalf("resuming from seq 2 gave %+v, want just ice-2 at seq 3", rest)
	}
}

// A call must leave no trace in the conversation. The signals are ephemeral and the message
// log is for things people said.
func TestCallSignalsAreNotPersistedAsMessages(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	_, aliceToken := f.user(t, "alice-np@pheme.test")
	bobID, _ := f.user(t, "bob-np@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	before := len(listMessages(t, f, aliceToken, conv))
	if code, _ := postSignal(t, f, aliceToken, conv, "call-np", []byte("offer"), true); code != http.StatusOK {
		t.Fatalf("signal: got %d", code)
	}
	if after := len(listMessages(t, f, aliceToken, conv)); after != before {
		t.Fatalf("a call signal was written to the message log (%d new); a call must leave no record", after-before)
	}
}

// The nudge on the live bus carries the call and the sequence — never the signal itself, and
// never anything the server could read anyway. It also names the caller, so a device does not
// ring for a call its own user placed on another device.
func TestCallSignalPublishesANudgeNotTheSignal(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	aliceID, aliceToken := f.user(t, "alice-nudge@pheme.test")
	bobID, _ := f.user(t, "bob-nudge@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	// Subscribed as BOB, the person the nudge is addressed to. The bus routes by recipient now, so
	// this asserts the nudge reaches the callee rather than merely that it was published.
	events, cancel := f.handler.Live.Subscribe(bobID)
	defer cancel()

	if code, _ := postSignal(t, f, aliceToken, conv, "call-n", []byte("super-secret-sdp"), false); code != http.StatusOK {
		t.Fatalf("signal: got %d", code)
	}

	e := <-events
	if e.CallSignal == nil {
		t.Fatal("expected a call signal event")
	}
	if e.CallSignal.CallID != "call-n" || e.CallSignal.Seq != 1 {
		t.Fatalf("nudge = %+v, want call-n at seq 1", e.CallSignal)
	}
	if e.CallSignal.FromUserID != aliceID {
		t.Fatalf("nudge must name the caller so their own other devices do not ring, got %q", e.CallSignal.FromUserID)
	}
	// The Recipients list is what lets the SSE handler authorise this without a database
	// query per event per subscriber — and signalling is chatty.
	if len(e.Recipients) != 2 {
		t.Fatalf("recipients = %v, want both members", e.Recipients)
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "super-secret-sdp") {
		t.Fatal("the signal itself rode the live bus; only a nudge should")
	}
	_ = bobID
}

// A callee whose live stream is down when the invite is published never hears the call. The
// invite is not lost — it sits in the mailbox for two minutes — but nothing looks at it again,
// so the call rings out against a device that was sitting right there. The caller re-nudges
// while it waits, and this is what that does.
func TestReRingingRepublishesTheNudgeWithoutTouchingTheMailbox(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	aliceID, aliceToken := f.user(t, "alice-rering@pheme.test")
	bobID, bobToken := f.user(t, "bob-rering@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := postSignal(t, f, aliceToken, conv, "call-r", []byte("the-invite"), true); code != http.StatusOK {
		t.Fatalf("invite: got %d", code)
	}

	events, cancel := f.handler.Live.Subscribe(bobID)
	defer cancel()

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/call-r/ring", aliceToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-ring: got %d (%s)", rec.Code, rec.Body.String())
	}

	e := <-events
	if e.CallSignal == nil || e.CallSignal.CallID != "call-r" {
		t.Fatalf("re-ring must republish the nudge, got %+v", e.CallSignal)
	}
	if e.CallSignal.FromUserID != aliceID {
		t.Fatalf("the nudge must still name the caller, so their own devices do not ring; got %q",
			e.CallSignal.FromUserID)
	}

	// It points at the invite; it does not add to it. A re-ring that appended would grow the
	// mailbox once every few seconds for as long as the phone rang, and the callee replays the
	// whole mailbox on every nudge.
	signals := getSignals(t, f, bobToken, conv, "call-r", 0)
	if len(signals) != 1 {
		t.Fatalf("mailbox has %d signals, want just the invite — a re-ring must not append",
			len(signals))
	}
}

// Ringing somebody is not something a stranger gets to do.
func TestReRingingRequiresMembership(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	_, aliceToken := f.user(t, "alice-rering-auth@pheme.test")
	bobID, _ := f.user(t, "bob-rering-auth@pheme.test")
	_, malloryToken := f.user(t, "mallory-rering@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/call-x/ring", malloryToken, nil)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("a non-member re-ringing a call: got %d, want it refused", rec.Code)
	}
}

// Exactly one device may answer.
//
// Every device a person is signed in on rings, and the loser has already opened its
// microphone. "Somebody else answered" cannot be delivered over a bus that is allowed to drop
// messages, so the answer is decided here — atomically — and both devices are told for
// certain. This is the one server-side lock in the whole feature.
func TestExactlyOneDeviceCanAnswerACall(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	_, aliceToken := f.user(t, "alice-ans@pheme.test")
	bobID, bobToken := f.user(t, "bob-ans@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)
	_ = bobID

	accept := func(device string) int {
		rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/call-a/accept", bobToken,
			map[string]any{"deviceId": device})
		return rec.Code
	}

	// Two of Bob's devices pick up in the same instant.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	devices := []string{"bob-phone", "bob-laptop"}
	wg.Add(2)
	for i := range devices {
		go func(i int) {
			defer wg.Done()
			codes[i] = accept(devices[i])
		}(i)
	}
	wg.Wait()

	won, lost := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			won++
		case http.StatusConflict:
			lost++
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("got %d winners and %d losers; exactly one device must answer", won, lost)
	}

	// The loser is told WHO won, so it can say "answered on another device" rather than just
	// stopping for no visible reason.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/call-a/accept", bobToken,
		map[string]any{"deviceId": "bob-tablet"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("a third device: got %d, want 409", rec.Code)
	}
	var out struct {
		Winner string `json:"winner"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Winner != "bob-phone" && out.Winner != "bob-laptop" {
		t.Fatalf("winner = %q, want one of Bob's two devices", out.Winner)
	}

	// And a device that retries its OWN successful claim — because the response was lost —
	// must not be told it lost a race against itself.
	if code := accept(out.Winner); code != http.StatusOK {
		t.Fatalf("the winner re-claiming: got %d, want 200", code)
	}
}

// Only a member of the conversation may signal into it or answer it. Otherwise a call is a
// way to make a stranger's phone ring.
func TestCallsRequireMembership(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	_, aliceToken := f.user(t, "alice-mem@pheme.test")
	bobID, _ := f.user(t, "bob-mem@pheme.test")
	_, outsiderToken := f.user(t, "outsider-mem@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := postSignal(t, f, outsiderToken, conv, "call-x", []byte("offer"), true); code == http.StatusOK {
		t.Fatal("an outsider must not be able to signal into a conversation")
	}
	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/calls/call-x/signals", outsiderToken, nil)
	if rec.Code == http.StatusOK {
		t.Fatal("an outsider must not be able to read a call's signals")
	}
	rec = f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/call-x/accept", outsiderToken,
		map[string]any{"deviceId": "d"})
	if rec.Code == http.StatusOK {
		t.Fatal("an outsider must not be able to answer a call")
	}
}

// With no mailbox configured, calling is off and the endpoints say so rather than half-working.
func TestCallEndpointsRefuseWhenCallingIsNotConfigured(t *testing.T) {
	f := newFixture(t) // no enableCalls()
	_, aliceToken := f.user(t, "alice-off@pheme.test")
	bobID, _ := f.user(t, "bob-off@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := postSignal(t, f, aliceToken, conv, "c", []byte("x"), false); code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 when calling is unconfigured", code)
	}
}

// Ringing wakes every device the callee has — that is what ringing means — and names only the
// caller. The server cannot say what the call is about and must not imply that it can.
func TestRingingNotifiesTheCalleesDevices(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	pusher := newFakePush()
	f.setPush(pusher)

	aliceID, aliceToken := f.user(t, "alice-ring@pheme.test")
	bobID, _ := f.user(t, "bob-ring@pheme.test")
	f.setDisplayName(t, aliceID, "Alice")
	bobPhone := f.device(t, bobID)
	f.device(t, aliceID) // the caller's own device must not ring
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := postSignal(t, f, aliceToken, conv, "call-r", []byte("offer"), true); code != http.StatusOK {
		t.Fatalf("invite: got %d", code)
	}
	if !pusher.waitForPush(t) {
		t.Fatal("expected the callee's devices to ring")
	}
	notes := pusher.notifications()
	if len(notes) != 1 || notes[0].Kind != push.KindCall || notes[0].CallID != "call-r" {
		t.Fatalf("ring = %+v, want one call notification for call-r", notes)
	}
	if notes[0].SenderName != "Alice" {
		t.Fatalf("ring must name the caller, got %q", notes[0].SenderName)
	}

	pusher.mu.Lock()
	devices := pusher.toDevs[0]
	pusher.mu.Unlock()
	if len(devices) != 1 || devices[0].ID != bobPhone {
		t.Fatalf("rang %+v; only the callee's devices should ring", devices)
	}
}

// Only the INVITE rings. The rest of a call's signalling — the answer, the hangup — goes over
// the live stream, and pushing for each one would buzz a phone half a dozen times per call.
func TestOnlyTheInviteRings(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-quiet@pheme.test")
	bobID, _ := f.user(t, "bob-quiet@pheme.test")
	f.device(t, bobID)
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := postSignal(t, f, aliceToken, conv, "call-q", []byte("ice"), false); code != http.StatusOK {
		t.Fatalf("signal: got %d", code)
	}
	if pusher.waitForPush(t) {
		t.Fatal("a signal that is not an invite must not ring anyone")
	}
}

// A call that stops ringing has to TAKE THE NOTIFICATION AWAY.
//
// Otherwise a missed call leaves a live-looking ring on the lock screen, and tapping it
// deep-links into a call nobody is on any more. The push that removes a notification is as much
// a part of ringing as the one that puts it there.
func TestACancelledCallClosesItsNotification(t *testing.T) {
	f := newFixture(t)
	f.enableCalls()
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-cancel@pheme.test")
	bobID, _ := f.user(t, "bob-cancel@pheme.test")
	f.device(t, bobID)
	conv := f.createDirect(t, aliceToken, bobID)

	postSignal(t, f, aliceToken, conv, "call-c", []byte("offer"), true)
	if !pusher.waitForPush(t) {
		t.Fatal("expected a ring")
	}

	// Alice gives up before Bob picks up.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/calls/call-c/signal", aliceToken,
		map[string]any{"ciphertext": []byte("hangup"), "cancel": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: got %d", rec.Code)
	}
	if !pusher.waitForPush(t) {
		t.Fatal("expected a cancel push to close the ring")
	}

	notes := pusher.notifications()
	if len(notes) != 2 {
		t.Fatalf("got %d notifications, want a ring and a cancel", len(notes))
	}
	if notes[1].Kind != push.KindCallCancel || notes[1].CallID != "call-c" {
		t.Fatalf("second notification = %+v, want a cancel for call-c", notes[1])
	}
}
