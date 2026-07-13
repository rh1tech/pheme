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
	if got := count(t, f, token, "dev-1"); got != 1 {
		t.Fatalf("after publishing 2 (one becomes last resort), consumable count = %d, want 1", got)
	}

	// Consume the single consumable one; the last resort remains but counts as zero.
	if rec := f.do(http.MethodGet, "/v1/mls/key-packages/"+userID+"/claim", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("claim: got %d", rec.Code)
	}
	if got := count(t, f, token, "dev-1"); got != 0 {
		t.Fatalf("consumable count after draining = %d, want 0 (so the client replenishes)", got)
	}
	// Yet a claim still succeeds, because the last resort is reusable.
	if rec := f.do(http.MethodGet, "/v1/mls/key-packages/"+userID+"/claim", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("claim on last resort: got %d", rec.Code)
	}
}

func publish(t *testing.T, f *fixture, token, deviceID string, packages [][]byte) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/mls/key-packages", token, map[string]any{
		"deviceId": deviceID, "keyPackages": packages,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("publish key packages: got %d (%s)", rec.Code, rec.Body.String())
	}
}

func count(t *testing.T, f *fixture, token, deviceID string) int {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/mls/key-packages/count?deviceId="+deviceID, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("count: got %d", rec.Code)
	}
	var out struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode count: %v", err)
	}
	return out.Count
}
