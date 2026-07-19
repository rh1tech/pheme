package chat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Withdrawing a device's KeyPackages.
//
// A device publishes KeyPackages so other members can add it to a group; deleting them is how a
// device that is going away stops being offered as a destination. Getting the scope wrong is
// expensive in a specific way this codebase has already paid for: a KeyPackage belonging to a
// device that no longer holds the private half is a zombie, and adding somebody to a group with one
// produces a member who can never decrypt anything.
//
// The scoping question is sharper than it looks because the device id is MINTED BY THE CLIENT.
// Nothing stops two different people's devices carrying the same id — it is a local identifier, not
// a server-issued one — so a delete keyed on the device id alone would reach across accounts.

// publishFor publishes a batch of KeyPackages for one of the caller's devices.
func publishFor(t *testing.T, f *fixture, token, deviceID string, n int) {
	t.Helper()
	packages := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		packages = append(packages, []byte{byte(i), 'k', 'p'})
	}
	rec := f.do(http.MethodPost, "/v1/mls/key-packages", token, map[string]any{
		"deviceId":             deviceID,
		"keyPackages":          packages,
		"lastResortKeyPackage": []byte("last-resort"),
	})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("publish for %s = %d: %s", deviceID, rec.Code, rec.Body)
	}
}

// countFor reads back what a device has published.
func countFor(t *testing.T, f *fixture, token, deviceID string) (int, bool) {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/mls/key-packages/count?deviceId="+deviceID, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("count for %s = %d: %s", deviceID, rec.Code, rec.Body)
	}
	var out struct {
		Count         int  `json:"count"`
		HasLastResort bool `json:"hasLastResort"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.Count, out.HasLastResort
}

func TestDeletingKeyPackagesClearsThatDevice(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "kp-delete@pheme.test")
	publishFor(t, f, token, "device-a", 3)

	if n, lastResort := countFor(t, f, token, "device-a"); n != 3 || !lastResort {
		t.Fatalf("setup: count=%d lastResort=%v, want 3 and true", n, lastResort)
	}

	rec := f.do(http.MethodDelete, "/v1/mls/key-packages?deviceId=device-a", token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body)
	}

	// The last-resort package goes too. It is the one that would keep being handed out forever.
	if n, lastResort := countFor(t, f, token, "device-a"); n != 0 || lastResort {
		t.Errorf("after deleting: count=%d lastResort=%v, want 0 and false — a package left behind "+
			"adds this device to groups it can no longer decrypt", n, lastResort)
	}
}

// A person's other devices are untouched. Withdrawing one device must not silently cost them the
// ability to be added on the rest.
func TestDeletingOneDevicesKeyPackagesLeavesTheOthers(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "kp-multi@pheme.test")
	publishFor(t, f, token, "phone", 2)
	publishFor(t, f, token, "laptop", 4)

	if rec := f.do(http.MethodDelete, "/v1/mls/key-packages?deviceId=phone", token, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}

	if n, _ := countFor(t, f, token, "phone"); n != 0 {
		t.Errorf("the deleted device still has %d packages", n)
	}
	if n, lastResort := countFor(t, f, token, "laptop"); n != 4 || !lastResort {
		t.Errorf("the other device lost packages: count=%d lastResort=%v, want 4 and true", n, lastResort)
	}
}

// THE ONE THAT MATTERS. Device ids are minted by clients, so two people can hold the same one. A
// delete must reach only the caller's.
func TestDeletingKeyPackagesCannotReachAnotherPersonsIdenticallyNamedDevice(t *testing.T) {
	f := newFixture(t)
	_, mine := f.user(t, "kp-mine@pheme.test")
	_, theirs := f.user(t, "kp-theirs@pheme.test")

	// The same client-minted id, on two unrelated accounts.
	const shared = "device-1"
	publishFor(t, f, mine, shared, 2)
	publishFor(t, f, theirs, shared, 5)

	if rec := f.do(http.MethodDelete, "/v1/mls/key-packages?deviceId="+shared, mine, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}

	if n, _ := countFor(t, f, mine, shared); n != 0 {
		t.Errorf("my own device kept %d packages", n)
	}
	if n, lastResort := countFor(t, f, theirs, shared); n != 5 || !lastResort {
		t.Errorf("another account's device with the same client-minted id lost packages: count=%d "+
			"lastResort=%v, want 5 and true — they would silently stop being addable to groups",
			n, lastResort)
	}
}

func TestDeletingKeyPackagesNeedsADeviceID(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "kp-nodevice@pheme.test")
	publishFor(t, f, token, "device-x", 1)

	if rec := f.do(http.MethodDelete, "/v1/mls/key-packages", token, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("delete with no deviceId = %d, want 400", rec.Code)
	}
	// And nothing was deleted on the way to refusing.
	if n, _ := countFor(t, f, token, "device-x"); n != 1 {
		t.Errorf("a refused delete removed packages anyway (count=%d)", n)
	}
}

// Deleting for a device that has published nothing is not an error — a client tidying up after
// itself must not have to know whether there was anything there.
func TestDeletingKeyPackagesForAnUnknownDeviceIsHarmless(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "kp-unknown@pheme.test")

	if rec := f.do(http.MethodDelete, "/v1/mls/key-packages?deviceId=never-published", token, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete for an unknown device = %d, want 204", rec.Code)
	}
}

func TestDeletingKeyPackagesRequiresSigningIn(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "kp-anon@pheme.test")
	publishFor(t, f, token, "device-anon", 2)

	if rec := f.do(http.MethodDelete, "/v1/mls/key-packages?deviceId=device-anon", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated delete = %d, want 401", rec.Code)
	}
	if n, _ := countFor(t, f, token, "device-anon"); n != 2 {
		t.Error("an unauthenticated request deleted the packages")
	}
}
