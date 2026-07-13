package chat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The server stores and returns the sealed key-backup blob verbatim, keeps one per
// user (a re-upload replaces the previous), scopes it to the owner, and 404s when
// none exists. It never inspects the ciphertext — that is the whole point.
func TestKeyBackupRoundTrip(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "backup@pheme.test")

	// No backup yet.
	if rec := f.do(http.MethodGet, "/v1/mls/key-backup", token, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 before any backup, got %d", rec.Code)
	}

	first := map[string]any{
		"deviceId":   "dev-1",
		"salt":       []byte("salt-aaaa"),
		"nonce":      []byte("nonce-aaaaaa"),
		"ciphertext": []byte("sealed-state-one"),
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, first); rec.Code != http.StatusNoContent {
		t.Fatalf("put backup: got %d", rec.Code)
	}

	got := getBackup(t, f, token)
	if string(got.Ciphertext) != "sealed-state-one" {
		t.Fatalf("ciphertext round-trip mismatch: %q", got.Ciphertext)
	}

	// A second upload replaces the first (one backup per user).
	second := map[string]any{
		"deviceId":   "dev-1",
		"salt":       []byte("salt-bbbb"),
		"nonce":      []byte("nonce-bbbbbb"),
		"ciphertext": []byte("sealed-state-two"),
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, second); rec.Code != http.StatusNoContent {
		t.Fatalf("replace backup: got %d", rec.Code)
	}
	if got := getBackup(t, f, token); string(got.Ciphertext) != "sealed-state-two" {
		t.Fatalf("expected replaced ciphertext, got %q", got.Ciphertext)
	}
}

func TestKeyBackupIsPerUser(t *testing.T) {
	f := newFixture(t)
	_, alice := f.user(t, "alice-b@pheme.test")
	_, bob := f.user(t, "bob-b@pheme.test")

	put := map[string]any{
		"deviceId": "dev", "salt": []byte("s"), "nonce": []byte("n"), "ciphertext": []byte("alice-secret"),
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", alice, put); rec.Code != http.StatusNoContent {
		t.Fatalf("alice put: got %d", rec.Code)
	}
	// Bob has his own (absent) backup — he never sees Alice's.
	if rec := f.do(http.MethodGet, "/v1/mls/key-backup", bob, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob must not see alice's backup, got %d", rec.Code)
	}
}

func TestKeyBackupRejectsEmpty(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "empty-b@pheme.test")
	bad := map[string]any{"deviceId": "dev", "salt": []byte{}, "nonce": []byte{}, "ciphertext": []byte{}}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty backup, got %d", rec.Code)
	}
}

type backupResponse struct {
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func getBackup(t *testing.T, f *fixture, token string) backupResponse {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/mls/key-backup", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get backup: got %d", rec.Code)
	}
	var out backupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	return out
}
