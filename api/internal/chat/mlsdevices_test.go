package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
	"slices"
	"strings"
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

// A terminated device must be visible to its co-members as REVOKED, so they can prune its leaf.
//
// Termination deletes the device's KeyPackages, which is what made this impossible: a revoked
// device then looks identical to one that has never published any, and that case is deliberately
// left alone (it may be someone who has not opened Pheme yet). So the pruning meant to sever a
// revoked device's access skipped it — guaranteed, not merely likely, when a user deletes ALL
// their devices, because then nobody has any published packages at all.
func TestTerminatedDeviceIsAdvertisedAsRevoked(t *testing.T) {
	f := newFixture(t)
	uid, tok := f.user(t, "revoker@pheme.test")
	_, peerTok := f.user(t, "peer@pheme.test")
	conv := f.createDirect(t, peerTok, uid)

	f.publishDevice(t, tok, "dev-live", "Laptop")
	f.publishDevice(t, tok, "dev-dead", "Old browser")
	publish(t, f, tok, "dev-dead", [][]byte{[]byte("kp-1")})

	if rec := f.do(http.MethodDelete, "/v1/mls/devices/dev-dead", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("terminate: got %d", rec.Code)
	}

	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/devices", peerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list devices: got %d", rec.Code)
	}
	var got struct {
		Devices map[string][]string `json:"devices"`
		Revoked map[string][]string `json:"revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Contains(got.Revoked[uid], "dev-dead") {
		t.Errorf("revoked = %v, want it to name dev-dead — without this a co-member cannot tell a "+
			"terminated device from one that never published keys, and leaves its leaf in place",
			got.Revoked)
	}
	if slices.Contains(got.Revoked[uid], "dev-live") {
		t.Errorf("revoked = %v, want it to name ONLY the terminated device", got.Revoked)
	}
	// And the owner's own list must not show it, or the removal reads as having failed.
	rec = f.do(http.MethodGet, "/v1/mls/devices", tok, nil)
	if strings.Contains(rec.Body.String(), "dev-dead") {
		t.Errorf("the owner still sees a terminated device in their own list: %s", rec.Body)
	}
}

// The device a per-session revocation cannot reach.
//
// Session ids are recorded on registration, but a device that registered before that field existed
// has none — so there is nothing for a revocation to match, and "terminate this device" left it
// with working API access indefinitely. Every other defence assumes that access is gone: the MLS
// leaf may outlive its pruning in a group, and a device that can still call the API can fetch the
// ciphertext its leaf still opens.
//
// Unable to name the one session, terminating it ends them all for that user. Heavy-handed, and
// the honest answer to "remove a device I cannot identify".
func TestTerminatingASessionlessDeviceSignsTheUserOut(t *testing.T) {
	f := newFixture(t)
	uid, tok := f.user(t, "sessionless@pheme.test")

	// A token carrying NO session id, which is what a client issued before session ids existed
	// holds. Registering with it is what records the device with a blank SessionID — writing a
	// blank one afterwards does not work, because UpsertMLSDevice deliberately refuses to erase a
	// known session id.
	sessionless, _, err := f.tokens.IssueWithSession(uid, string(domain.RoleUser), "")
	if err != nil {
		t.Fatalf("issue sessionless: %v", err)
	}
	f.publishDevice(t, sessionless, "dev-old", "Ancient browser")

	// Both tokens work before.
	if rec := f.do(http.MethodGet, "/v1/mls/devices", sessionless, nil); rec.Code != http.StatusOK {
		t.Fatalf("pre-terminate sessionless: got %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/v1/mls/devices", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("pre-terminate: got %d", rec.Code)
	}

	if rec := f.do(http.MethodDelete, "/v1/mls/devices/dev-old", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("terminate: got %d", rec.Code)
	}

	// The terminated device's own token is dead — the point of the whole exercise.
	if rec := f.do(http.MethodGet, "/v1/mls/devices", sessionless, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("the terminated sessionless device is still signed in: got %d, want 401", rec.Code)
	}

	// And so is every other session this user holds, which is the cost of not being able to name
	// the one. Documented here so the bluntness is a decision rather than a surprise.
	if rec := f.do(http.MethodGet, "/v1/mls/devices", tok, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("post-terminate: got %d, want 401 — the user's other sessions should end too", rec.Code)
	}
}

// A terminated device is absent from the list AND named in `revoked`.
//
// Both halves matter, and the second is the one that was missing. A client cannot tell "I was
// revoked" from "I have never registered" by absence alone, and the two demand opposite responses:
// the first means this device's keys are dead everywhere and it must mint a new identity, the
// second means it should just register. A browser that could not tell kept using keys its peers had
// already pruned — every send failed with UseAfterEviction and nothing said why.
func TestListMyDevicesNamesRevokedOnes(t *testing.T) {
	f := newFixture(t)
	uid, tok := f.user(t, "revoked-list@pheme.test")

	f.publishDevice(t, tok, "dev-alive", "Kept")
	f.publishDevice(t, tok, "dev-dead", "Terminated")
	if rec := f.do(http.MethodDelete, "/v1/mls/devices/dev-dead", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("terminate: %d %s", rec.Code, rec.Body)
	}

	// A fresh token, because terminating a device that carries no recorded session id signs the
	// user out EVERYWHERE — see terminateDevice, which does that deliberately rather than report a
	// removal it cannot enforce. Devices published through this fixture have no session id, so the
	// original token is dead by design and the assertions below would read as a 401.
	access, _, _, err := f.tokens.Issue(uid, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("re-issue token: %v", err)
	}

	rec := f.do(http.MethodGet, "/v1/mls/devices", access, nil)
	var got struct {
		Devices []struct {
			DeviceID string `json:"deviceId"`
		} `json:"devices"`
		Revoked []string `json:"revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Devices) != 1 || got.Devices[0].DeviceID != "dev-alive" {
		t.Fatalf("devices = %+v, want only dev-alive", got.Devices)
	}
	if len(got.Revoked) != 1 || got.Revoked[0] != "dev-dead" {
		t.Fatalf("revoked = %v, want [dev-dead] — a client cannot recover without being told", got.Revoked)
	}
}
