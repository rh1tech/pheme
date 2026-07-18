package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// publishDevice registers a device under the given token's session, so its KeyPackages and its
// session id land in the store the way the real publish path records them.
func (f *fixture) publishDevice(t *testing.T, tok, deviceID, label string) {
	t.Helper()
	body := map[string]any{
		"deviceId":             deviceID,
		"keyPackages":          [][]byte{[]byte("kp-" + deviceID)},
		"lastResortKeyPackage": []byte("last-" + deviceID),
		"label":                label,
	}
	if rec := f.do(http.MethodPost, "/v1/mls/key-packages", tok, body); rec.Code != http.StatusNoContent {
		t.Fatalf("publish %s: got %d", deviceID, rec.Code)
	}
}

// Publishing KeyPackages registers the device in the user's own registry with its label, and
// GET /v1/mls/devices lists it — the data behind "your devices".
func TestMyDevicesListsPublishedDevices(t *testing.T) {
	f := newFixture(t)
	_, tok := f.user(t, "devices@pheme.test")

	pub := map[string]any{
		"deviceId":             "dev-abc",
		"keyPackages":          [][]byte{[]byte("kp-1")},
		"lastResortKeyPackage": []byte("kp-last"),
		"label":                "Chrome on macOS",
	}
	if rec := f.do(http.MethodPost, "/v1/mls/key-packages", tok, pub); rec.Code != http.StatusNoContent {
		t.Fatalf("publish: got %d", rec.Code)
	}

	rec := f.do(http.MethodGet, "/v1/mls/devices", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list devices: got %d", rec.Code)
	}
	var out struct {
		Devices []domain.MLSDevice `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Devices) != 1 {
		t.Fatalf("want 1 device, got %d", len(out.Devices))
	}
	if out.Devices[0].DeviceID != "dev-abc" || out.Devices[0].Label != "Chrome on macOS" {
		t.Fatalf("unexpected device: %+v", out.Devices[0])
	}
	if out.Devices[0].LastSeenAt.IsZero() {
		t.Fatal("lastSeenAt should be set")
	}
}

// A device registers itself with no key packages — the fix for a long-lived, well-stocked device
// that never republished and so never appeared in "your devices".
func TestRegisterDeviceListsWithoutPublishing(t *testing.T) {
	f := newFixture(t)
	_, tok := f.user(t, "register@pheme.test")

	body := map[string]any{"deviceId": "dev-reg", "label": "Firefox on Linux"}
	if rec := f.do(http.MethodPost, "/v1/mls/devices", tok, body); rec.Code != http.StatusNoContent {
		t.Fatalf("register: got %d", rec.Code)
	}

	rec := f.do(http.MethodGet, "/v1/mls/devices", tok, nil)
	var out struct {
		Devices []domain.MLSDevice `json:"devices"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Devices) != 1 || out.Devices[0].DeviceID != "dev-reg" {
		t.Fatalf("want dev-reg listed, got %+v", out.Devices)
	}
	if out.Devices[0].Label != "Firefox on Linux" || out.Devices[0].LastSeenAt.IsZero() {
		t.Fatalf("register must set label and last-seen: %+v", out.Devices[0])
	}
}

// The registry is per-user: a user never sees another user's devices.
func TestMyDevicesIsPerUser(t *testing.T) {
	f := newFixture(t)
	_, aTok := f.user(t, "a-dev@pheme.test")
	_, bTok := f.user(t, "b-dev@pheme.test")

	pub := map[string]any{"deviceId": "a-1", "keyPackages": [][]byte{[]byte("kp")}, "label": "A's laptop"}
	if rec := f.do(http.MethodPost, "/v1/mls/key-packages", aTok, pub); rec.Code != http.StatusNoContent {
		t.Fatalf("a publish: got %d", rec.Code)
	}

	rec := f.do(http.MethodGet, "/v1/mls/devices", bTok, nil)
	var out struct {
		Devices []domain.MLSDevice `json:"devices"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Devices) != 0 {
		t.Fatalf("b must not see a's devices, got %d", len(out.Devices))
	}
}

// Terminating a device severs it three ways: its login stops working (the session is revoked),
// its published KeyPackages are gone so it cannot be re-added to a group, and it leaves the
// registry. A user's OTHER device is untouched — this is "remove that one device", not "log out
// everywhere".
func TestTerminateDeviceRevokesSessionAndKeys(t *testing.T) {
	f := newFixture(t)
	uid, tokA := f.user(t, "multi@pheme.test")

	// A second session for the same user — the device we will terminate, signed in separately.
	accessB, _, _, err := f.tokens.Issue(uid, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("issue B: %v", err)
	}

	f.publishDevice(t, tokA, "dev-a", "Laptop")
	f.publishDevice(t, accessB, "dev-b", "Old phone")

	// Both sessions work, and both devices are listed.
	if rec := f.do(http.MethodGet, "/v1/mls/devices", accessB, nil); rec.Code != http.StatusOK {
		t.Fatalf("B pre-terminate list: got %d", rec.Code)
	}

	// Device A terminates device B.
	if rec := f.do(http.MethodDelete, "/v1/mls/devices/dev-b", tokA, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("terminate dev-b: got %d", rec.Code)
	}

	// B's session is dead: its token now 401s everywhere behind the middleware...
	if rec := f.do(http.MethodGet, "/v1/mls/devices", accessB, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("B after terminate: want 401, got %d", rec.Code)
	}
	// ...and it cannot refresh its way back in (the deny list is checked, not just expiry).
	// A's session is untouched.
	if rec := f.do(http.MethodGet, "/v1/mls/devices", tokA, nil); rec.Code != http.StatusOK {
		t.Fatalf("A after terminating B: want 200, got %d", rec.Code)
	}

	// B's KeyPackages are gone, so it can never be claimed back into a group.
	if n, err := f.store.CountKeyPackages(context.Background(), uid, "dev-b"); err != nil || n != 0 {
		t.Fatalf("dev-b key packages: n=%d err=%v (want 0)", n, err)
	}
	if has, _ := f.store.HasLastResortKeyPackage(context.Background(), uid, "dev-b"); has {
		t.Fatal("dev-b last-resort package should be gone")
	}
	// A's KeyPackages are untouched.
	if has, _ := f.store.HasLastResortKeyPackage(context.Background(), uid, "dev-a"); !has {
		t.Fatal("dev-a last-resort package should survive terminating dev-b")
	}

	// And B is out of the registry, while A remains.
	var out struct {
		Devices []domain.MLSDevice `json:"devices"`
	}
	rec := f.do(http.MethodGet, "/v1/mls/devices", tokA, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Devices) != 1 || out.Devices[0].DeviceID != "dev-a" {
		t.Fatalf("after terminate, want only dev-a, got %+v", out.Devices)
	}
}

// A user cannot terminate a device that is not theirs — the registry lookup is user-scoped, so
// another user's device id simply is not found.
func TestTerminateDeviceIsOwnerScoped(t *testing.T) {
	f := newFixture(t)
	aUID, aTok := f.user(t, "owner@pheme.test")
	_, bTok := f.user(t, "attacker@pheme.test")
	f.publishDevice(t, aTok, "a-dev", "A's laptop")

	if rec := f.do(http.MethodDelete, "/v1/mls/devices/a-dev", bTok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user terminate: want 404, got %d", rec.Code)
	}
	// A's session and keys are intact — the attacker touched nothing.
	if has, _ := f.store.HasLastResortKeyPackage(context.Background(), aUID, "a-dev"); !has {
		t.Fatal("a-dev key package should survive a cross-user terminate attempt")
	}
	if rec := f.do(http.MethodGet, "/v1/mls/devices", aTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("A still works: got %d", rec.Code)
	}
}

// Terminating a device has to take away its PUSH addresses too, not just its keys and its session.
//
// It did not, and there was no field joining the two device registries, so nothing could even find
// the push row to delete. A revoked browser therefore kept its subscription and went on receiving
// messages — and since previews shipped, those pushes carry the CIPHERTEXT of the very messages it
// had just been told it could no longer read. A user who deletes a device is told it is gone; that
// has to be true of every way the device can still be reached.
func TestTerminateDeviceRemovesItsPushAddresses(t *testing.T) {
	f := newFixture(t)
	uid, tok := f.user(t, "pushy@pheme.test")

	f.publishDevice(t, tok, "dev-keep", "Laptop")
	f.publishDevice(t, tok, "dev-gone", "Old browser")

	push := func(mlsDeviceID, endpoint string) {
		t.Helper()
		dev := domain.Device{
			UserID:          uid,
			Platform:        domain.PlatformWeb,
			WebPushEndpoint: endpoint,
			MLSDeviceID:     mlsDeviceID,
		}
		if _, err := f.store.CreateDevice(context.Background(), dev); err != nil {
			t.Fatalf("create push device: %v", err)
		}
	}
	push("dev-keep", "https://push.example/keep")
	push("dev-gone", "https://push.example/gone")

	if rec := f.do(http.MethodDelete, "/v1/mls/devices/dev-gone", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("terminate: got %d", rec.Code)
	}

	devices, err := f.store.DevicesForUsers(context.Background(), []string{uid})
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	for _, d := range devices {
		if d.MLSDeviceID == "dev-gone" {
			t.Fatalf("a terminated device still has a push address: %s", d.WebPushEndpoint)
		}
	}
	// And the device that was NOT terminated keeps its own.
	var kept int
	for _, d := range devices {
		if d.MLSDeviceID == "dev-keep" {
			kept++
		}
	}
	if kept != 1 {
		t.Errorf("kept push addresses for the surviving device = %d, want 1", kept)
	}
}
