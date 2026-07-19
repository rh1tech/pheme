package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Web Push against a real HTTP push service — a fake one, but a real server over a real socket,
// because what is being tested is how the sends are ORDERED IN TIME, and that is invisible to a
// test that stubs the transport out.
//
// This path used to be a sequential loop: one round-trip per device, each waiting for the last,
// inside a fifteen-second budget, while holding one of the process's sixty-four push slots. A
// conversation of ten people with two devices each meant twenty round-trips end to end. When the
// slots run out the server drops notifications outright, so a slow send is not just slow for its
// own notification — it is why somebody else's went missing.

// vapid returns a usable key pair for the tests.
func vapid(t *testing.T) (private, public string) {
	t.Helper()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	return priv, pub
}

// subscriptionFor builds a valid PushSubscription JSON pointing at endpoint. The keys must be real:
// the library encrypts the payload to them, and rejects nonsense before any request is made.
func subscriptionFor(t *testing.T, endpoint string) string {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("auth secret: %v", err)
	}
	return fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`,
		endpoint,
		base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(auth),
	)
}

func webDevices(t *testing.T, endpoint string, n int) []domain.Device {
	t.Helper()
	devices := make([]domain.Device, 0, n)
	for i := 0; i < n; i++ {
		devices = append(devices, domain.Device{
			ID:         fmt.Sprintf("device-%d", i),
			Platform:   domain.PlatformWeb,
			WebPushSub: subscriptionFor(t, endpoint),
		})
	}
	return devices
}

// THE ONE THAT MATTERS. Twenty slow devices must not take twenty times as long as one.
func TestWebPushSendsToDevicesConcurrently(t *testing.T) {
	const devices = 20
	const perRequest = 100 * time.Millisecond

	var inFlight, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := inFlight.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(perRequest)
		inFlight.Add(-1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	priv, pub := vapid(t)
	sender := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "")

	start := time.Now()
	results, err := sender.SendChat(context.Background(),
		ChatNotification{SenderName: "Alice", ConversationID: "conv1", MessageID: "m1"},
		webDevices(t, srv.URL, devices))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}
	if len(results) != devices {
		t.Fatalf("got %d results for %d devices", len(results), devices)
	}
	for i, r := range results {
		if r.Status != domain.DeliverySent {
			t.Errorf("device %d: status %q, error %q", i, r.Status, r.Error)
		}
	}

	if peak.Load() < 2 {
		t.Errorf("peak concurrency was %d — the sends are still serialised", peak.Load())
	}
	// Sequentially this is 20 × 100ms = 2s. Concurrently it is a handful of batches. The threshold
	// is deliberately loose: the claim is "not serialised", not a specific speed.
	if sequential := devices * perRequest; elapsed > sequential/2 {
		t.Errorf("sending to %d devices took %s; sequential would be %s, so this is barely better. "+
			"Each of those round-trips holds a push slot the whole time.", devices, elapsed, sequential)
	}
}

// Concurrency must not scramble which result belongs to which device — a mixed-up Gone flag would
// delete the registration of a device that is working perfectly well.
func TestWebPushResultsStayWithTheirDevices(t *testing.T) {
	// Every third device's endpoint reports 410 Gone.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("gone") == "1" {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	var devices []domain.Device
	wantGone := map[string]bool{}
	for i := 0; i < 12; i++ {
		endpoint := srv.URL
		gone := i%3 == 0
		if gone {
			endpoint += "?gone=1"
		}
		id := fmt.Sprintf("device-%d", i)
		wantGone[id] = gone
		devices = append(devices, domain.Device{
			ID: id, Platform: domain.PlatformWeb, WebPushSub: subscriptionFor(t, endpoint),
		})
	}

	priv, pub := vapid(t)
	sender := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "")
	results, err := sender.SendChat(context.Background(), ChatNotification{SenderName: "Alice", ConversationID: "conv1", MessageID: "m1"}, devices)
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}
	if len(results) != len(devices) {
		t.Fatalf("got %d results for %d devices", len(results), len(devices))
	}

	for i, r := range results {
		if r.DeviceID != devices[i].ID {
			t.Fatalf("result %d belongs to %q but is in %q's position; a Gone flag landing on the "+
				"wrong device deletes a working registration", i, r.DeviceID, devices[i].ID)
		}
		if r.Gone != wantGone[r.DeviceID] {
			t.Errorf("%s: Gone=%v, want %v", r.DeviceID, r.Gone, wantGone[r.DeviceID])
		}
	}
}

// A device with no subscription is skipped without a request, and still gets its own result in its
// own position.
func TestWebPushSkipsDevicesWithNoSubscription(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	priv, pub := vapid(t)
	sender := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "")

	devices := []domain.Device{
		{ID: "no-sub", Platform: domain.PlatformWeb},
		{ID: "has-sub", Platform: domain.PlatformWeb, WebPushSub: subscriptionFor(t, srv.URL)},
		{ID: "junk-sub", Platform: domain.PlatformWeb, WebPushSub: "not json"},
	}
	results, err := sender.SendChat(context.Background(), ChatNotification{SenderName: "Alice", ConversationID: "conv1", MessageID: "m1"}, devices)
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].DeviceID != "no-sub" || results[0].Status != domain.DeliverySkipped {
		t.Errorf("device with no subscription: %+v", results[0])
	}
	if results[1].DeviceID != "has-sub" || results[1].Status != domain.DeliverySent {
		t.Errorf("device with a subscription: %+v", results[1])
	}
	if results[2].DeviceID != "junk-sub" || results[2].Status != domain.DeliveryFailed {
		t.Errorf("device with an unparseable subscription: %+v", results[2])
	}
	if results[2].Gone {
		t.Error("an unparseable subscription must not be reported as Gone — that deletes the device " +
			"row on what may be a bug in how it was stored")
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d HTTP requests made, want 1 — only one device had somewhere to send to", n)
	}
}

// The concurrency is BOUNDED. Unbounded, one notification to a large group opens a connection per
// device to whichever push services its members use, which is how a sender gets rate-limited.
func TestWebPushConcurrencyIsBounded(t *testing.T) {
	var inFlight, peak atomic.Int64
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := inFlight.Add(1)
		mu.Lock()
		if now > peak.Load() {
			peak.Store(now)
		}
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		inFlight.Add(-1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	priv, pub := vapid(t)
	sender := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "")

	// Far more devices than the bound allows at once.
	if _, err := sender.SendChat(context.Background(), ChatNotification{SenderName: "Alice", ConversationID: "conv1", MessageID: "m1"},
		webDevices(t, srv.URL, webPushConcurrency*4)); err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	if got := peak.Load(); got > int64(webPushConcurrency) {
		t.Errorf("%d requests were in flight at once, bound is %d", got, webPushConcurrency)
	}
}

// A cancelled context must stop the run promptly rather than working through every remaining
// device. The caller's budget has expired; the notification is already late.
func TestWebPushStopsWhenTheContextIsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	priv, pub := vapid(t)
	sender := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	results, err := sender.SendChat(ctx, ChatNotification{SenderName: "Alice", ConversationID: "conv1", MessageID: "m1"},
		webDevices(t, srv.URL, webPushConcurrency*3))
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a cancelled send took %s to give up", elapsed)
	}
	if len(results) != webPushConcurrency*3 {
		t.Errorf("got %d results, want one per device even when cancelled", len(results))
	}
}

// The channel broadcast path. SendChat was covered and this was not, which is the wrong way round
// by volume: a conversation has a handful of members, a channel has as many subscribers as it can
// get, and this is the call that fans out to all of them.
func TestWebPushSendsAChannelMessageToEveryDevice(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	priv, pub := vapid(t)
	sender := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "https://pheme.example")

	devices := webDevices(t, srv.URL, 5)
	results, err := sender.Send(context.Background(), domain.Message{
		ID: "msg-1", ChannelID: "chan-1", Title: "Deploy finished", Body: "v1.2.3 is live",
		Images: []domain.MessageImage{{ID: "img-1", Width: 8, Height: 8}},
	}, devices)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(results) != len(devices) {
		t.Fatalf("got %d results for %d devices", len(results), len(devices))
	}
	for i, r := range results {
		if r.Status != domain.DeliverySent {
			t.Errorf("device %d: status %q, error %q", i, r.Status, r.Error)
		}
		if r.DeviceID != devices[i].ID {
			t.Errorf("result %d belongs to %q but sits in %q's position", i, r.DeviceID, devices[i].ID)
		}
	}
	if n := requests.Load(); n != int64(len(devices)) {
		t.Errorf("%d requests for %d devices; a subscriber was skipped", n, len(devices))
	}
}

// A channel send also prunes: a subscriber whose browser dropped the subscription reports 410, and
// that must be distinguishable from one whose delivery merely failed.
func TestWebPushChannelSendReportsGoneSeparatelyFromFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("outcome") {
		case "gone":
			w.WriteHeader(http.StatusGone)
		case "error":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	priv, pub := vapid(t)
	sender := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "")

	devices := []domain.Device{
		{ID: "ok", Platform: domain.PlatformWeb, WebPushSub: subscriptionFor(t, srv.URL)},
		{ID: "dropped", Platform: domain.PlatformWeb, WebPushSub: subscriptionFor(t, srv.URL+"?outcome=gone")},
		{ID: "flaky", Platform: domain.PlatformWeb, WebPushSub: subscriptionFor(t, srv.URL+"?outcome=error")},
	}
	results, err := sender.Send(context.Background(),
		domain.Message{ID: "m", ChannelID: "c", Title: "t"}, devices)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	byID := map[string]Result{}
	for _, r := range results {
		byID[r.DeviceID] = r
	}
	if got := byID["ok"]; got.Status != domain.DeliverySent || got.Gone {
		t.Errorf("the working subscriber: %+v", got)
	}
	if got := byID["dropped"]; !got.Gone {
		t.Errorf("a dropped subscription was not reported as Gone (%+v); it stays in the collection "+
			"and every future broadcast retries it forever", got)
	}
	if got := byID["flaky"]; got.Gone {
		t.Errorf("a transient 500 was reported as Gone (%+v); the subscriber loses their "+
			"registration over a bad minute", got)
	}
}

// Connections must be REUSED across notifications.
//
// webpush-go supplies its own &http.Client{} when none is given, which falls back to
// http.DefaultTransport and its MaxIdleConnsPerHost of 2. Every notification past the second
// concurrent one to the same host then paid for a fresh TCP connection and a full TLS handshake,
// and left a socket in TIME_WAIT behind it. Against FCM — one host for every Android device on the
// server — that is a handshake per notification. It showed up first as a load test exhausting the
// machine's ephemeral ports.
//
// Counting distinct connections is the only way to see this: every notification succeeds either
// way, so nothing else distinguishes a pooled client from one that reconnects every time.
func TestWebPush_ReusesConnectionsAcrossNotifications(t *testing.T) {
	var mu sync.Mutex
	conns := map[string]bool{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		conns[r.RemoteAddr] = true
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	priv, pub := vapid(t)
	s := NewWebPushSender(pub, priv, "mailto:ops@pheme.test", "")
	devices := webDevices(t, srv.URL, 40)

	// Sequential rounds, so concurrency cannot be what keeps connections distinct: with a working
	// pool these should almost all land on connections opened by the first round.
	for round := 0; round < 3; round++ {
		if _, err := s.SendChat(context.Background(), ChatNotification{SenderName: "A"}, devices); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}

	mu.Lock()
	used := len(conns)
	mu.Unlock()

	// 120 notifications went out. Bounded concurrency is webPushConcurrency, so a pooled client
	// needs about that many connections; an unpooled one opens one per notification.
	if used > webPushConcurrency*2 {
		t.Errorf("120 notifications used %d distinct connections, want no more than %d. Each extra "+
			"one is a TCP connection and a TLS handshake that the pool should have made unnecessary, "+
			"and a socket left in TIME_WAIT — which is what exhausts ephemeral ports under load.",
			used, webPushConcurrency*2)
	}
}
