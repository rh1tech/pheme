package chat

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func tailEntry(conversation, message, sealed string) map[string]any {
	return map[string]any{
		"conversationId": conversation,
		"messageId":      message,
		"salt":           []byte("salt-aaaa"),
		"nonce":          []byte("nonce-aaaa"),
		"ciphertext":     []byte(sealed),
	}
}

func appendTail(t *testing.T, f *fixture, token string, entries ...map[string]any) int {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/mls/key-backup/tail", token, map[string]any{
		"deviceId": "dev-1",
		"entries":  entries,
	})
	return rec.Code
}

func readTail(t *testing.T, f *fixture, token string) []domain.MLSKeyBackupTailEntry {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/mls/key-backup/tail", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get tail: %d — %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Entries []domain.MLSKeyBackupTailEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode tail: %v", err)
	}
	return body.Entries
}

// A body appended the moment it exists comes back byte for byte. This is the path that closes the
// window the snapshot's debounce left open, and everything in it is the only copy of a message.
func TestBackupTailRoundTrip(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "tail@pheme.test")

	if entries := readTail(t, f, token); len(entries) != 0 {
		t.Fatalf("expected an empty tail to start, got %d", len(entries))
	}
	if code := appendTail(t, f, token, tailEntry("c1", "m1", "sealed-one")); code != http.StatusNoContent {
		t.Fatalf("append: %d", code)
	}

	entries := readTail(t, f, token)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if string(entries[0].Ciphertext) != "sealed-one" {
		t.Errorf("ciphertext = %q", entries[0].Ciphertext)
	}
	if string(entries[0].Salt) == "" || string(entries[0].Nonce) == "" {
		t.Errorf("an entry without its salt and nonce can never be opened: %+v", entries[0])
	}
}

// One user must never be handed another's bodies, sealed or not — the ciphertext is theirs and
// the conversation and message ids alone say who talks to whom.
func TestBackupTailIsPerUser(t *testing.T) {
	f := newFixture(t)
	_, mine := f.user(t, "tail-mine@pheme.test")
	_, theirs := f.user(t, "tail-theirs@pheme.test")

	if code := appendTail(t, f, mine, tailEntry("c1", "m1", "mine")); code != http.StatusNoContent {
		t.Fatalf("append: %d", code)
	}
	if entries := readTail(t, f, theirs); len(entries) != 0 {
		t.Fatalf("another user's tail leaked %d entries", len(entries))
	}
}

// An entry missing any part of what it takes to open it must be refused at the door. Accepting it
// would store a body that reports as backed up and can never be read — the exact failure the tail
// exists to prevent, reintroduced one layer down.
func TestBackupTailRefusesUnopenableEntries(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "tail-bad@pheme.test")

	for name, entry := range map[string]map[string]any{
		"no conversation": {"messageId": "m1", "salt": []byte("s"), "nonce": []byte("n"), "ciphertext": []byte("c")},
		"no message":      {"conversationId": "c1", "salt": []byte("s"), "nonce": []byte("n"), "ciphertext": []byte("c")},
		"no ciphertext":   {"conversationId": "c1", "messageId": "m1", "salt": []byte("s"), "nonce": []byte("n")},
		"no salt":         {"conversationId": "c1", "messageId": "m1", "nonce": []byte("n"), "ciphertext": []byte("c")},
		"no nonce":        {"conversationId": "c1", "messageId": "m1", "salt": []byte("s"), "ciphertext": []byte("c")},
	} {
		t.Run(name, func(t *testing.T) {
			if code := appendTail(t, f, token, entry); code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", code)
			}
		})
	}
	if entries := readTail(t, f, token); len(entries) != 0 {
		t.Fatalf("a refused entry was stored anyway: %d", len(entries))
	}
}

// Appending is idempotent, so a client that never learned whether its append landed can simply
// send it again. Without this the only safe client is one that never retries, and a body lost to
// a dropped connection is lost for good.
func TestBackupTailAppendIsIdempotent(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "tail-retry@pheme.test")

	for i := 0; i < 3; i++ {
		if code := appendTail(t, f, token, tailEntry("c1", "m1", "sealed-one")); code != http.StatusNoContent {
			t.Fatalf("append %d: %d", i, code)
		}
	}
	if entries := readTail(t, f, token); len(entries) != 1 {
		t.Fatalf("three identical appends stored %d entries", len(entries))
	}
}

// A snapshot absorbs the tail up to the moment the client began reading, and only that far.
// Anything appended DURING the upload is not in the snapshot and must survive.
func TestCheckpointTruncatesOnlyWhatItAbsorbed(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "tail-checkpoint@pheme.test")

	if code := appendTail(t, f, token, tailEntry("c1", "m1", "before")); code != http.StatusNoContent {
		t.Fatalf("append: %d", code)
	}
	// The client starts reading its cache here. The pause is what makes the two appends land on
	// distinguishable timestamps — the boundary is a real instant, not an ordering.
	time.Sleep(5 * time.Millisecond)
	began := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)
	if code := appendTail(t, f, token, tailEntry("c1", "m2", "during the upload")); code != http.StatusNoContent {
		t.Fatalf("append: %d", code)
	}

	rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, map[string]any{
		"deviceId":             "dev-1",
		"salt":                 []byte("salt-aaaa"),
		"nonce":                []byte("nonce-aaaa"),
		"ciphertext":           []byte("sealed-state"),
		"transcriptSalt":       []byte("t-salt"),
		"transcriptNonce":      []byte("t-nonce"),
		"transcriptCiphertext": []byte("sealed-transcript"),
		"transcriptMessages":   10,
		"tailThrough":          began,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("checkpoint: %d — %s", rec.Code, rec.Body.String())
	}

	entries := readTail(t, f, token)
	if len(entries) != 1 {
		t.Fatalf("want 1 surviving entry, got %d", len(entries))
	}
	if entries[0].MessageID != "m2" {
		t.Errorf("the checkpoint dropped a body it does not contain: kept %s", entries[0].MessageID)
	}
}

// A checkpoint that says nothing about the tail must leave it entirely alone — an older client
// talking to a newer server must not lose bodies for not knowing about the field.
func TestCheckpointWithoutTailThroughKeepsTheTail(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "tail-oldclient@pheme.test")

	if code := appendTail(t, f, token, tailEntry("c1", "m1", "keep me")); code != http.StatusNoContent {
		t.Fatalf("append: %d", code)
	}
	rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, map[string]any{
		"deviceId":   "dev-1",
		"salt":       []byte("salt-aaaa"),
		"nonce":      []byte("nonce-aaaa"),
		"ciphertext": []byte("sealed-state"),
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("checkpoint: %d", rec.Code)
	}
	if entries := readTail(t, f, token); len(entries) != 1 {
		t.Fatalf("the tail was truncated by a client that never asked: %d", len(entries))
	}
}

// THE SHRINK GUARD, EXTENDED. Stored history is the snapshot plus everything appended since, so a
// replacement must clear both. Counting only the snapshot would wave through a backup that
// silently drops every body written since the last checkpoint.
func TestShrinkGuardCountsTheTail(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "tail-guard@pheme.test")

	snapshot := func(messages int, force bool) int {
		body := map[string]any{
			"deviceId":             "dev-1",
			"salt":                 []byte("salt-aaaa"),
			"nonce":                []byte("nonce-aaaa"),
			"ciphertext":           []byte("sealed-state"),
			"transcriptSalt":       []byte("t-salt"),
			"transcriptNonce":      []byte("t-nonce"),
			"transcriptCiphertext": []byte("sealed-transcript"),
			"transcriptMessages":   messages,
		}
		if force {
			body["force"] = true
		}
		return f.do(http.MethodPut, "/v1/mls/key-backup", token, body).Code
	}

	if code := snapshot(10, false); code != http.StatusNoContent {
		t.Fatalf("first snapshot: %d", code)
	}
	// Five more bodies arrive and are appended. Stored history is now 15.
	for i := 0; i < 5; i++ {
		if code := appendTail(t, f, token, tailEntry("c1", "m"+string(rune('a'+i)), "sealed")); code != http.StatusNoContent {
			t.Fatalf("append %d: %d", i, code)
		}
	}

	// A device holding only the 10 it restored must not be able to replace 15 without saying so.
	if code := snapshot(10, false); code != http.StatusConflict {
		t.Fatalf("expected 409 for a snapshot that drops the tail, got %d", code)
	}
	// The tail is untouched by a refusal — a rejected upload must never cost history.
	if entries := readTail(t, f, token); len(entries) != 5 {
		t.Fatalf("a refused snapshot took %d tail entries with it", 5-len(entries))
	}
	// A device that genuinely holds all 15 is fine.
	if code := snapshot(15, false); code != http.StatusNoContent {
		t.Fatalf("a snapshot holding everything was refused: %d", code)
	}
	// And a person who means it can still force.
	if code := snapshot(1, true); code != http.StatusNoContent {
		t.Fatalf("forced snapshot: %d", code)
	}
}
