package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/blob"
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

// The sealed blobs live in the blob store, and replacing a backup must not leave the old
// ones behind: one user keeps exactly the blobs their current backup references, however many
// times they re-upload. This is what keeps "back up often" from growing storage without bound.
func TestKeyBackupReplacesBlobsWithoutOrphans(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "orphan-b@pheme.test")
	blobs := f.handler.Blobs.(*blob.Memory)

	put := func(state, transcript string) {
		body := map[string]any{
			"deviceId":             "dev",
			"salt":                 []byte("salt-aaaa"),
			"nonce":                []byte("nonce-aaaaaa"),
			"ciphertext":           []byte(state),
			"transcriptSalt":       []byte("tsalt"),
			"transcriptNonce":      []byte("tnonce"),
			"transcriptCiphertext": []byte(transcript),
		}
		if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, body); rec.Code != http.StatusNoContent {
			t.Fatalf("put backup: got %d", rec.Code)
		}
	}

	put("state-1", "transcript-1")
	if n := blobs.Len(); n != 2 {
		t.Fatalf("after first backup, want 2 blobs (state+transcript), got %d", n)
	}
	// Three more uploads. If old blobs leaked, this would climb to 8.
	put("state-2", "transcript-2")
	put("state-3", "transcript-3")
	put("state-4", "transcript-4")
	if n := blobs.Len(); n != 2 {
		t.Fatalf("after four backups, want 2 blobs (no orphans), got %d", n)
	}

	// And the latest is what restores.
	got := getBackup(t, f, token)
	if string(got.Ciphertext) != "state-4" || string(got.TranscriptCiphertext) != "transcript-4" {
		t.Fatalf("restored the wrong version: %q / %q", got.Ciphertext, got.TranscriptCiphertext)
	}
}

// A backup larger than a MongoDB document's 16MB ceiling — the exact thing the old
// inline-in-the-document design could not store. With the blobs in the blob store, size is a
// non-issue.
func TestKeyBackupAcceptsLargerThanAMongoDocument(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "big-b@pheme.test")

	big := bytes.Repeat([]byte("x"), 20*1024*1024) // 20MB, past the 16MB doc limit
	body := map[string]any{
		"deviceId":   "dev",
		"salt":       []byte("salt-aaaa"),
		"nonce":      []byte("nonce-aaaaaa"),
		"ciphertext": big,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, body); rec.Code != http.StatusNoContent {
		t.Fatalf("a 20MB backup must be accepted, got %d", rec.Code)
	}
	if got := getBackup(t, f, token); len(got.Ciphertext) != len(big) {
		t.Fatalf("large backup round-trip: got %d bytes, want %d", len(got.Ciphertext), len(big))
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
	Salt                 []byte `json:"salt"`
	Nonce                []byte `json:"nonce"`
	Ciphertext           []byte `json:"ciphertext"`
	TranscriptCiphertext []byte `json:"transcriptCiphertext"`
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
