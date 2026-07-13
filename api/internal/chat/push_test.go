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
		DisplayName: name,
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

// An MLS Welcome is protocol traffic, not a message a human sent: it must not
// produce a notification.
func TestChatPushSkipsControlMessages(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-c@pheme.test")
	bobID, _ := f.user(t, "bob-c@pheme.test")
	f.device(t, bobID)
	conv := f.createDirect(t, aliceToken, bobID)

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", aliceToken, map[string]any{
		"ciphertext":  []byte("opaque-welcome"),
		"contentType": "application/mls-welcome",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send welcome: got %d", rec.Code)
	}

	if pusher.waitForPush(t) {
		t.Fatal("a Welcome control message must not notify anyone")
	}
	if n := len(pusher.notifications()); n != 0 {
		t.Fatalf("expected no notifications, got %d", n)
	}
}
