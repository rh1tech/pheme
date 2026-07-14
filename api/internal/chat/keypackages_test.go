package chat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// KeyPackages are single-use, so without a last-resort package a fellow member could
// claim someone's whole stock in a loop and leave them un-messageable. The last-resort
// package is handed out but never consumed, so a claim always succeeds and being added
// to a group can never be denied.
//
// Note what the server can and cannot do here: it records WHICH package is last-resort,
// but the property that makes it reusable lives in the bytes the client built (an RFC
// 9420 extension telling that client to keep the private key). The crate's own tests
// cover that half; this covers the directory's half.
func TestKeyPackagesCannotBeDrained(t *testing.T) {
	f := newFixture(t)
	victimID, victimToken := f.user(t, "victim@pheme.test")
	_, attackerToken := f.user(t, "attacker@pheme.test")
	conv := f.createDirect(t, attackerToken, victimID)

	publish(t, f, victimToken, "dev-1", [][]byte{[]byte("kp-1"), []byte("kp-2"), []byte("kp-3")})

	// Drain far past the published stock.
	for i := 0; i < 20; i++ {
		got := claim(t, f, attackerToken, conv, http.StatusOK, deviceRef{UserID: victimID, DeviceID: "dev-1"})
		if len(got) != 1 || len(got[0].KeyPackage) == 0 {
			t.Fatalf("claim %d: expected the victim to remain reachable, got %+v", i, got)
		}
	}
}

// The key directory is scoped to a conversation you are IN.
//
// Unscoped, any signed-in stranger could enumerate a victim's devices and stand in a loop
// draining their single-use KeyPackages — never making them unreachable (the last-resort
// package sees to that) but permanently pinning them to it, so every join they are ever
// given reuses one init key and quietly gives up the forward secrecy the single-use
// packages exist to provide. There is no reason to let anyone claim keys for someone they
// are not talking to.
func TestKeyDirectoryIsScopedToTheConversation(t *testing.T) {
	f := newFixture(t)
	victimID, victimToken := f.user(t, "victim-s@pheme.test")
	aliceID, aliceToken := f.user(t, "alice-s@pheme.test")
	_, strangerToken := f.user(t, "stranger-s@pheme.test")

	publish(t, f, victimToken, "dev-1", [][]byte{[]byte("kp")})
	// Alice and the victim are talking; the stranger is in a conversation of their own.
	conv := f.createDirect(t, aliceToken, victimID)
	strangerConv := f.createDirect(t, strangerToken, aliceID)

	// A stranger cannot reach into a conversation they are not a member of at all. (They
	// are told it does not exist rather than that they are not in it — a conversation they
	// have no part in should not be something they can even confirm the existence of.)
	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/devices", strangerToken, nil)
	if rec.Code == http.StatusOK {
		t.Fatal("a non-member must not be able to list a conversation's devices")
	}
	claim(t, f, strangerToken, conv, http.StatusNotFound, deviceRef{UserID: victimID, DeviceID: "dev-1"})

	// Nor can they use a conversation they ARE in to claim keys for somebody who is not.
	claim(t, f, strangerToken, strangerConv, http.StatusForbidden,
		deviceRef{UserID: victimID, DeviceID: "dev-1"})

	// The victim's stock is untouched, so Alice — who is actually talking to them — can
	// still reach every one of their devices.
	got := claim(t, f, aliceToken, conv, http.StatusOK, deviceRef{UserID: victimID, DeviceID: "dev-1"})
	if len(got) != 1 {
		t.Fatalf("a member of the conversation must be able to claim, got %+v", got)
	}
}

// A user signed in on two devices has TWO leaves in any group they are in, so a claim
// must hand back a package for each. Handing back one package for the user — which is
// what the directory used to do — puts one arbitrary device of theirs in the group and
// leaves every other device they own unable to read a word of the conversation.
func TestClaimIsPerDeviceNotPerUser(t *testing.T) {
	f := newFixture(t)
	bobID, bobToken := f.user(t, "bob@pheme.test")
	_, aliceToken := f.user(t, "alice@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	publish(t, f, bobToken, "phone", [][]byte{[]byte("phone-kp")})
	publish(t, f, bobToken, "laptop", [][]byte{[]byte("laptop-kp")})

	// Alice asks which devices Bob has. Nothing is consumed by asking.
	if got := devices(t, f, aliceToken, conv)[bobID]; len(got) != 2 || got[0] != "laptop" || got[1] != "phone" {
		t.Fatalf("Bob's devices = %v, want both [laptop phone]", got)
	}

	// And she can claim a package for each, so both of Bob's devices become leaves.
	got := claim(t, f, aliceToken, conv, http.StatusOK,
		deviceRef{UserID: bobID, DeviceID: "phone"},
		deviceRef{UserID: bobID, DeviceID: "laptop"},
	)
	if len(got) != 2 {
		t.Fatalf("claimed %d key packages, want one per device", len(got))
	}
	for _, c := range got {
		if len(c.KeyPackage) == 0 {
			t.Fatalf("device %s came back with no key package", c.DeviceID)
		}
	}
}

// A member who has never opened Pheme must not stop a group forming with everyone else:
// their device is simply absent from the claim, not an error.
func TestClaimSkipsDevicesThatPublishedNothing(t *testing.T) {
	f := newFixture(t)
	bobID, bobToken := f.user(t, "bob2@pheme.test")
	_, aliceToken := f.user(t, "alice2@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	publish(t, f, bobToken, "phone", [][]byte{[]byte("kp")})

	got := claim(t, f, aliceToken, conv, http.StatusOK,
		deviceRef{UserID: bobID, DeviceID: "phone"},
		deviceRef{UserID: bobID, DeviceID: "a-device-that-never-published"},
	)
	if len(got) != 1 || got[0].DeviceID != "phone" {
		t.Fatalf("claimed %+v, want only Bob's reachable device", got)
	}

	// But if NOTHING we asked for is reachable there is no group to build, and the caller
	// has to be told so rather than shown a generic failure.
	claim(t, f, aliceToken, conv, http.StatusNotFound,
		deviceRef{UserID: bobID, DeviceID: "a-device-that-never-published"})
}

// The count reports only consumable packages: the last-resort one is never used up,
// so counting it would make the client think it has stock and stop replenishing.
func TestKeyPackageCountExcludesLastResort(t *testing.T) {
	f := newFixture(t)
	userID, token := f.user(t, "counter@pheme.test")
	peerID, peerToken := f.user(t, "counter-peer@pheme.test")
	conv := f.createDirect(t, peerToken, userID)
	_ = peerID

	publish(t, f, token, "dev-1", [][]byte{[]byte("a"), []byte("b")})
	if got, _ := count(t, f, token, "dev-1"); got != 2 {
		t.Fatalf("consumable count = %d, want 2 (the last-resort package is separate)", got)
	}

	// Drain both consumable ones.
	for i := 0; i < 2; i++ {
		claim(t, f, peerToken, conv, http.StatusOK, deviceRef{UserID: userID, DeviceID: "dev-1"})
	}
	got, hasLastResort := count(t, f, token, "dev-1")
	if got != 0 {
		t.Fatalf("consumable count after draining = %d, want 0 (so the client replenishes)", got)
	}
	if !hasLastResort {
		t.Fatal("the last-resort package must survive being handed out")
	}
	// And a claim still succeeds, because the last resort is reusable.
	claim(t, f, peerToken, conv, http.StatusOK, deviceRef{UserID: userID, DeviceID: "dev-1"})
}

// A device publishes exactly one last-resort package; a second is refused rather
// than stored where it could never be handed out.
func TestSecondLastResortIsIgnored(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "dupe@pheme.test")

	publish(t, f, token, "dev-1", nil)
	publish(t, f, token, "dev-1", nil)
	if _, hasLastResort := count(t, f, token, "dev-1"); !hasLastResort {
		t.Fatal("expected the device to have a last-resort package")
	}
	// The second publish must not have added consumable stock either.
	if got, _ := count(t, f, token, "dev-1"); got != 0 {
		t.Fatalf("consumable count = %d, want 0", got)
	}
}

// publish sends a batch plus this device's last-resort package. The bytes are opaque
// to the server, so a placeholder stands in for a real one here.
func publish(t *testing.T, f *fixture, token, deviceID string, packages [][]byte) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/mls/key-packages", token, map[string]any{
		"deviceId":             deviceID,
		"keyPackages":          packages,
		"lastResortKeyPackage": []byte("last-resort-kp"),
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("publish key packages: got %d (%s)", rec.Code, rec.Body.String())
	}
}

type claimedPackage struct {
	UserID     string `json:"userId"`
	DeviceID   string `json:"deviceId"`
	KeyPackage []byte `json:"keyPackage"`
}

// claim asks for one KeyPackage per named device, within a conversation, and asserts the
// status.
func claim(t *testing.T, f *fixture, token, conv string, want int, refs ...deviceRef) []claimedPackage {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/key-packages/claim", token,
		map[string]any{"devices": refs})
	if rec.Code != want {
		t.Fatalf("claim: got %d, want %d (%s)", rec.Code, want, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		return nil
	}
	var out struct {
		KeyPackages []claimedPackage `json:"keyPackages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	return out.KeyPackages
}

// devices lists the publishing devices of every member of a conversation.
func devices(t *testing.T, f *fixture, token, conv string) map[string][]string {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/devices", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list devices: got %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Devices map[string][]string `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode devices: %v", err)
	}
	return out.Devices
}

func count(t *testing.T, f *fixture, token, deviceID string) (int, bool) {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/mls/key-packages/count?deviceId="+deviceID, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("count: got %d", rec.Code)
	}
	var out struct {
		Count         int  `json:"count"`
		HasLastResort bool `json:"hasLastResort"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode count: %v", err)
	}
	return out.Count, out.HasLastResort
}
