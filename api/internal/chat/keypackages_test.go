package chat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// KeyPackages are single-use, so without a last-resort package any stranger could
// claim a user's whole stock in a loop and leave them un-messageable. The
// last-resort package is handed out but never consumed, so a claim always succeeds
// and starting an encrypted chat with someone can never be denied by an attacker.
//
// Note what the server can and cannot do here: it records WHICH package is
// last-resort, but the property that makes it reusable lives in the bytes the client
// built (an RFC 9420 extension telling that client to keep the private key). The
// crate's own tests cover that half; this covers the directory's half.
func TestKeyPackagesCannotBeDrained(t *testing.T) {
	f := newFixture(t)
	victimID, victimToken := f.user(t, "victim@pheme.test")
	_, attackerToken := f.user(t, "attacker@pheme.test")

	publish(t, f, victimToken, "dev-1", [][]byte{[]byte("kp-1"), []byte("kp-2"), []byte("kp-3")})

	// Drain far past the published stock.
	for i := 0; i < 20; i++ {
		rec := f.do(http.MethodGet, "/v1/mls/key-packages/"+victimID+"/claim", attackerToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("claim %d: expected the victim to remain reachable, got %d", i, rec.Code)
		}
		var out struct {
			KeyPackage []byte `json:"keyPackage"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode claim %d: %v", i, err)
		}
		if len(out.KeyPackage) == 0 {
			t.Fatalf("claim %d returned an empty key package", i)
		}
	}
}

// The count reports only consumable packages: the last-resort one is never used up,
// so counting it would make the client think it has stock and stop replenishing.
func TestKeyPackageCountExcludesLastResort(t *testing.T) {
	f := newFixture(t)
	userID, token := f.user(t, "counter@pheme.test")

	publish(t, f, token, "dev-1", [][]byte{[]byte("a"), []byte("b")})
	if got, _ := count(t, f, token, "dev-1"); got != 2 {
		t.Fatalf("consumable count = %d, want 2 (the last-resort package is separate)", got)
	}

	// Drain both consumable ones.
	for i := 0; i < 2; i++ {
		if rec := f.do(http.MethodGet, "/v1/mls/key-packages/"+userID+"/claim", token, nil); rec.Code != http.StatusOK {
			t.Fatalf("claim %d: got %d", i, rec.Code)
		}
	}
	got, hasLastResort := count(t, f, token, "dev-1")
	if got != 0 {
		t.Fatalf("consumable count after draining = %d, want 0 (so the client replenishes)", got)
	}
	if !hasLastResort {
		t.Fatal("the last-resort package must survive being handed out")
	}
	// And a claim still succeeds, because the last resort is reusable.
	if rec := f.do(http.MethodGet, "/v1/mls/key-packages/"+userID+"/claim", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("claim on last resort: got %d", rec.Code)
	}
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
