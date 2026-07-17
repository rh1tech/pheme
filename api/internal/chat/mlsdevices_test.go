package chat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

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
