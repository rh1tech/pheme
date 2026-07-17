package channel

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

// appFixture wires the AppHandler with in-memory dependencies and a real token
// manager so tests exercise the full route + JWT-middleware path.
type appFixture struct {
	mux    *http.ServeMux
	tokens *auth.TokenManager
	store  store.Store
	pub    *broker.Memory
	blob   blob.Store
	h      *AppHandler
}

func newAppFixture(t *testing.T) *appFixture {
	t.Helper()
	blobs := blob.NewMemory()
	db := store.NewMemory(blobs)
	tokens := auth.NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)
	pub := broker.NewMemory(8)
	h := &AppHandler{
		Store:     db,
		Live:      live.NewMemoryBus(),
		Tokens:    tokens,
		Publisher: pub,
		Blob:      blobs,
		Admin:     &AdminHandler{Store: db},
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	return &appFixture{mux: mux, tokens: tokens, store: db, pub: pub, blob: blobs, h: h}
}

// tokenFor issues an access token for a freshly created user and returns it.
func (f *appFixture) tokenFor(t *testing.T, email string) (string, domain.User) {
	t.Helper()
	u := seedUser(t, f.store, email, domain.RoleUser)
	access, _, _, err := f.tokens.Issue(u.ID, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return access, u
}

func (f *appFixture) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		r = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestAppProtectedRequiresToken(t *testing.T) {
	f := newAppFixture(t)
	rec := f.do(http.MethodGet, "/v1/channels", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAppCreateAndListChannels(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")

	rec := f.do(http.MethodPost, "/v1/channels", token,
		map[string]any{"name": "Alerts", "subscriptionMode": "open"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var created domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Name != "Alerts" || created.SubscriptionMode != domain.ModeOpen {
		t.Fatalf("unexpected channel: %+v", created)
	}

	rec = f.do(http.MethodGet, "/v1/channels", token, nil)
	var list struct {
		Channels []domain.Channel `json:"channels"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Channels) != 1 || list.Channels[0].ID != created.ID {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestAppCreateChannelRequiresName(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	rec := f.do(http.MethodPost, "/v1/channels", token, map[string]any{"name": ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAppOwnershipEnforcedOnOtherUsersChannel(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, _ := f.tokenFor(t, "owner@b.com")
	rec := f.do(http.MethodPost, "/v1/channels", ownerToken, map[string]any{"name": "Mine"})
	var ch domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)

	intruderToken, _ := f.tokenFor(t, "intruder@b.com")
	rec = f.do(http.MethodDelete, "/v1/channels/"+ch.ID, intruderToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAppCreateListRevokeKey(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	rec := f.do(http.MethodPost, "/v1/channels", token, map[string]any{"name": "Keys"})
	var ch domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)

	// Create a key — plaintext returned exactly once.
	rec = f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/keys", token, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create key status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var key struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &key)
	if key.Key == "" || key.ID == "" {
		t.Fatalf("expected plaintext key and id, got %+v", key)
	}

	// List shows the key (without the secret).
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/keys", token, nil)
	var keys struct {
		Keys []domain.APIKey `json:"keys"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &keys)
	if len(keys.Keys) != 1 {
		t.Fatalf("want 1 key, got %d", len(keys.Keys))
	}

	// Revoke it.
	rec = f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/keys/"+key.ID, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

func TestAppNotifyChannelEnqueues(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	rec := f.do(http.MethodPost, "/v1/channels", token, map[string]any{"name": "Send"})
	var ch domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)

	rec = f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token,
		map[string]any{"title": "Hello", "body": "World"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("notify status = %d, want 202; body=%s", rec.Code, rec.Body)
	}

	// Empty payload is rejected.
	rec = f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token,
		map[string]any{"title": "", "body": ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty notify status = %d, want 400", rec.Code)
	}
}

func TestAppNotifyRejectsNonOwner(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, _ := f.tokenFor(t, "owner@b.com")
	rec := f.do(http.MethodPost, "/v1/channels", ownerToken, map[string]any{"name": "Send"})
	var ch domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)

	otherToken, _ := f.tokenFor(t, "other@b.com")
	rec = f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", otherToken,
		map[string]any{"title": "x"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAppCreateDeviceAndSubscribe(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	rec := f.do(http.MethodPost, "/v1/channels", token,
		map[string]any{"name": "Sub", "subscriptionMode": "open"})
	var ch domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)

	rec = f.do(http.MethodPost, "/v1/devices", token, map[string]any{"platform": "web"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("device status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var dev domain.Device
	_ = json.Unmarshal(rec.Body.Bytes(), &dev)

	rec = f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/subscribe", token,
		map[string]any{"deviceId": dev.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("subscribe status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	// Owner subscribing to an open channel is active immediately.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/subscription?deviceId="+dev.ID, token, nil)
	var sub struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sub)
	if sub.Status != "active" {
		t.Fatalf("subscription status = %q, want active", sub.Status)
	}
}

func TestAppHealthIsPublic(t *testing.T) {
	f := newAppFixture(t)
	rec := f.do(http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
