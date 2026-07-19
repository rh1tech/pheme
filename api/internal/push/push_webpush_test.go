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
