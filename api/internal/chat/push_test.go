package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/push"
)

// fakePush records what the server tried to notify, so a test can assert on it.
// Note it can only ever record a ChatNotification, which has no content field —
// the type makes leaking plaintext into a push impossible, and this test guards
// the surrounding behaviour: who gets notified, and for which messages.
type fakePush struct {
	mu     sync.Mutex
	sent   []push.ChatNotification
	toDevs [][]domain.Device
	fired  chan struct{}
}

func newFakePush() *fakePush { return &fakePush{fired: make(chan struct{}, 8)} }

func (f *fakePush) Send(context.Context, domain.Message, []domain.Device) ([]push.Result, error) {
	return nil, nil
}

func (f *fakePush) SendChat(_ context.Context, n push.ChatNotification, devices []domain.Device) ([]push.Result, error) {
	f.mu.Lock()
	f.sent = append(f.sent, n)
	f.toDevs = append(f.toDevs, devices)
	f.mu.Unlock()
	f.fired <- struct{}{}
	return nil, nil
}

func (f *fakePush) notifications() []push.ChatNotification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]push.ChatNotification(nil), f.sent...)
}

// waitForPush waits for the background push goroutine, or reports none arrived.
func (f *fakePush) waitForPush(t *testing.T) bool {
	t.Helper()
	select {
	case <-f.fired:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func (f *fixture) setPush(p push.Sender) { f.handler.Push = p }

func (f *fixture) setICE(c ICEConfig) { f.handler.ICE = c }

// device registers one push-capable device for a user.
func (f *fixture) device(t *testing.T, userID string) string {
	t.Helper()
	d, err := f.store.CreateDevice(context.Background(), domain.Device{
		UserID:   userID,
		Platform: "web",
		FCMToken: "token-" + userID,
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	return d.ID
}

func (f *fixture) setDisplayName(t *testing.T, userID, name string) {
	t.Helper()
	_, err := f.store.UpdateUserProfile(context.Background(), userID, domain.UserProfileUpdate{
		DisplayName: &name,
	})
	if err != nil {
		t.Fatalf("set display name: %v", err)
	}
}

// createDirect starts a direct chat and returns its id.
func (f *fixture) createDirect(t *testing.T, token, otherID string) string {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations", token, map[string]any{
		"kind": "direct", "memberIds": []string{otherID},
	})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create direct: got %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	return out.ID
}

// A chat message notifies the other member — naming the sender, never the content
// (which the server cannot read). The sender is never notified of their own message.
func TestChatPushNotifiesOtherMemberOnly(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	aliceID, aliceToken := f.user(t, "alice-p@pheme.test")
	bobID, _ := f.user(t, "bob-p@pheme.test")
	f.setDisplayName(t, aliceID, "Alice")
	bobDevice := f.device(t, bobID)
	f.device(t, aliceID) // the sender's own device must not be notified

	conv := f.createDirect(t, aliceToken, bobID)

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceToken, map[string]any{
		"ciphertext":  []byte("opaque-mls-ciphertext"),
		"contentType": "application/mls",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: got %d", rec.Code)
	}

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	notes := pusher.notifications()
	if len(notes) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notes))
	}
	if notes[0].SenderName != "Alice" {
		t.Errorf("sender name: got %q, want %q", notes[0].SenderName, "Alice")
	}
	if notes[0].ConversationID != conv {
		t.Errorf("conversation id: got %q, want %q", notes[0].ConversationID, conv)
	}

	pusher.mu.Lock()
	devices := pusher.toDevs[0]
	pusher.mu.Unlock()
	if len(devices) != 1 || devices[0].ID != bobDevice {
		t.Fatalf("expected only bob's device to be notified, got %+v", devices)
	}
}

// MLS protocol traffic is not a message a human sent: it must never buzz anyone's
// phone. A Welcome and a Commit both travel through the commit endpoint.
func TestChatPushSkipsControlMessages(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-c@pheme.test")
	bobID, _ := f.user(t, "bob-c@pheme.test")
	f.device(t, bobID)
	conv := f.createDirect(t, aliceToken, bobID)

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", aliceToken, map[string]any{
		"groupId":   "grp-1",
		"baseEpoch": 0,
		"welcome":   []byte("opaque-welcome"),
		"commit":    []byte("opaque-commit"),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("post commit: got %d (%s)", rec.Code, rec.Body.String())
	}

	if pusher.waitForPush(t) {
		t.Fatal("MLS control messages must not notify anyone")
	}
	if n := len(pusher.notifications()); n != 0 {
		t.Fatalf("expected no notifications, got %d", n)
	}
}

// A Welcome or Commit posted through the ORDINARY message route would skip the epoch
// compare-and-set, putting a Commit into the log that the group never agreed to —
// and forking every member who applied it. The route must refuse them.
func TestControlMessagesCannotBypassTheCommitEndpoint(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-d@pheme.test")
	bobID, _ := f.user(t, "bob-d@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	for _, ct := range []string{"application/mls-welcome", "application/mls-commit"} {
		rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceToken, map[string]any{
			"ciphertext":  []byte("opaque"),
			"contentType": ct,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s through the message route: got %d, want 400", ct, rec.Code)
		}
	}
}

// clearPrivacy removes a user's stored notification privacy value, reproducing an account row
// created before the setting existed.
//
// It writes the empty value through UpdateUserProfile rather than re-creating the user, because
// CreateUser deliberately fills the default in — that is the mechanism this test exists to
// verify, so going around it would test nothing.
func (f *fixture) clearPrivacy(t *testing.T, userID string) {
	t.Helper()
	legacy := domain.NotificationPrivacy("")
	if _, err := f.store.UpdateUserProfile(context.Background(), userID, domain.UserProfileUpdate{
		NotificationPrivacy: &legacy,
	}); err != nil {
		t.Fatalf("clear privacy: %v", err)
	}
}

// capableDevice registers a device whose build can decrypt and draw a preview.
func (f *fixture) capableDevice(t *testing.T, userID string) string {
	t.Helper()
	d, err := f.store.CreateDevice(context.Background(), domain.Device{
		UserID:           userID,
		Platform:         "android",
		FCMToken:         "token-capable-" + userID,
		CanRenderPreview: true,
	})
	if err != nil {
		t.Fatalf("create capable device: %v", err)
	}
	return d.ID
}

// A push address the push service has declared permanently dead has to be removed, or it is
// retried on every message for the life of the account.
//
// Nothing used to read the Result. FCM answered UNREGISTERED for rotated tokens and uninstalled
// apps, the web push service answered 410 for dropped subscriptions, and the rows stayed — one
// phone accumulated four, three of which could never receive anything.
func TestDeadPushAddressesArePruned(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	dead, err := f.store.CreateDevice(ctx, domain.Device{
		UserID:   "u1",
		Platform: domain.PlatformAndroid,
		FCMToken: "rotated-away",
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	live, err := f.store.CreateDevice(ctx, domain.Device{
		UserID:   "u1",
		Platform: domain.PlatformAndroid,
		FCMToken: "still-good",
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	f.handler.pruneDeadDevices(ctx, []push.Result{
		{DeviceID: dead.ID, Status: domain.DeliveryFailed, Gone: true, Error: "UNREGISTERED"},
		// A plain failure must NOT cost a device its registration: the network being down for a
		// moment is not the same as the address being dead.
		{DeviceID: live.ID, Status: domain.DeliveryFailed, Error: "timeout"},
	})

	devices, err := f.store.DevicesForUsers(ctx, []string{"u1"})
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	var sawDead, sawLive bool
	for _, d := range devices {
		if d.ID == dead.ID {
			sawDead = true
		}
		if d.ID == live.ID {
			sawLive = true
		}
	}
	if sawDead {
		t.Error("a permanently dead push address survived; it will be retried forever")
	}
	if !sawLive {
		t.Error("a device was pruned for a transient failure")
	}
}

// reset forgets what has been recorded, so a test can assert on a SECOND message without the first
// one's deliveries still in the way.
func (f *fakePush) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
	f.toDevs = nil
	for len(f.fired) > 0 {
		<-f.fired
	}
}
