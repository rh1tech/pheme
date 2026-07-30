package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// The sealed blobs live in the blob store, and storage must stay bounded however many times a
// device re-uploads.
//
// This used to assert that a replaced backup's blobs were deleted immediately. They are not any
// more, deliberately: the superseded backup is archived so that replacing one is not the same act
// as destroying it — see putKeyBackup. What has to hold now is the weaker but sufficient property,
// that the total stops growing once the archive is full. The old assertion was the reason a
// replaced history could never be recovered.
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

	// The live backup plus a bounded archive: one live and keptBackupVersions superseded, each
	// holding a state and a transcript.
	put("state-2", "transcript-2")
	put("state-3", "transcript-3")
	put("state-4", "transcript-4")
	full := 2 * (1 + keptBackupVersions)
	if n := blobs.Len(); n != full {
		t.Fatalf("after four backups, want %d blobs (live + %d archived), got %d",
			full, keptBackupVersions, n)
	}

	// THE BOUND. Past this point every upload prunes one version out of the archive and its blobs
	// go with it, so the total holds steady rather than climbing forever.
	for i := 5; i <= 12; i++ {
		put(fmt.Sprintf("state-%d", i), fmt.Sprintf("transcript-%d", i))
	}
	if n := blobs.Len(); n != full {
		t.Fatalf("after twelve backups, want %d blobs still, got %d — the prune is leaking", full, n)
	}

	// And the latest is what restores.
	got := getBackup(t, f, token)
	if string(got.Ciphertext) != "state-12" || string(got.TranscriptCiphertext) != "transcript-12" {
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
	TranscriptSalt       []byte `json:"transcriptSalt"`
	TranscriptNonce      []byte `json:"transcriptNonce"`
	TranscriptCiphertext []byte `json:"transcriptCiphertext"`
	TranscriptMessages   int    `json:"transcriptMessages"`
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

// The transcript is the only copy of a decrypted history, and there is one backup per user
// which the upload handler replaces in place. A device that has read nothing must therefore
// not be able to seal an empty transcript over a full one.
//
// This is not a hypothetical: a freshly installed phone did exactly that, and the history was
// gone for good — the recovery code still opened the backup, the backup just no longer held
// anything. Every case below is a step of that sequence.
func TestKeyBackupRefusesToShrinkTheTranscript(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "shrink@pheme.test")

	full := map[string]any{
		"deviceId":             "the-device-that-had-the-history",
		"salt":                 []byte("salt-aaaa"),
		"nonce":                []byte("nonce-aaaaaa"),
		"ciphertext":           []byte("sealed-state"),
		"transcriptSalt":       []byte("t-salt"),
		"transcriptNonce":      []byte("t-nonce"),
		"transcriptCiphertext": []byte("a hundred messages, sealed"),
		"transcriptMessages":   100,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, full); rec.Code != http.StatusNoContent {
		t.Fatalf("seed backup: got %d", rec.Code)
	}

	// The freshly installed device: same account, same recovery code, no history read yet.
	empty := map[string]any{
		"deviceId":   "freshly-installed",
		"salt":       []byte("salt-bbbb"),
		"nonce":      []byte("nonce-bbbbbb"),
		"ciphertext": []byte("sealed-state-2"),
		// No transcript at all — backupKeys sends none when the cache is empty.
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, empty); rec.Code != http.StatusConflict {
		t.Fatalf("an empty transcript replaced a full one: got %d, want 409", rec.Code)
	}

	// A transcript that is merely SMALLER is refused for the same reason.
	fewer := map[string]any{
		"deviceId":             "freshly-installed",
		"salt":                 []byte("salt-cccc"),
		"nonce":                []byte("nonce-cccccc"),
		"ciphertext":           []byte("sealed-state-3"),
		"transcriptSalt":       []byte("t-salt-2"),
		"transcriptNonce":      []byte("t-nonce-2"),
		"transcriptCiphertext": []byte("three messages"),
		"transcriptMessages":   3,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, fewer); rec.Code != http.StatusConflict {
		t.Fatalf("a smaller transcript replaced a larger one: got %d, want 409", rec.Code)
	}

	// The stored backup is untouched by either refusal — the history is still there.
	got := getBackup(t, f, token)
	if string(got.TranscriptCiphertext) != "a hundred messages, sealed" {
		t.Fatalf("the stored transcript was damaged by a refused upload: %q", got.TranscriptCiphertext)
	}

	// Growing is always allowed: that is the normal case, a device that has read more.
	more := map[string]any{
		"deviceId":             "the-device-that-had-the-history",
		"salt":                 []byte("salt-dddd"),
		"nonce":                []byte("nonce-dddddd"),
		"ciphertext":           []byte("sealed-state-4"),
		"transcriptSalt":       []byte("t-salt-3"),
		"transcriptNonce":      []byte("t-nonce-3"),
		"transcriptCiphertext": []byte("a hundred and one messages, sealed"),
		"transcriptMessages":   101,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, more); rec.Code != http.StatusNoContent {
		t.Fatalf("a larger transcript was refused: got %d", rec.Code)
	}

	// And a person who genuinely means it — having cleared their history, say — can still say so.
	forced := map[string]any{
		"deviceId":             "freshly-installed",
		"salt":                 []byte("salt-eeee"),
		"nonce":                []byte("nonce-eeeeee"),
		"ciphertext":           []byte("sealed-state-5"),
		"transcriptSalt":       []byte("t-salt-4"),
		"transcriptNonce":      []byte("t-nonce-4"),
		"transcriptCiphertext": []byte("deliberately fewer"),
		"transcriptMessages":   1,
		"force":                true,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, forced); rec.Code != http.StatusNoContent {
		t.Fatalf("a forced replacement was refused: got %d", rec.Code)
	}
	if got := getBackup(t, f, token); string(got.TranscriptCiphertext) != "deliberately fewer" {
		t.Fatalf("force did not replace: %q", got.TranscriptCiphertext)
	}
}

// The very first backup has nothing to be compared against and must always be accepted.
func TestKeyBackupFirstUploadIsNeverRefused(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "first@pheme.test")

	first := map[string]any{
		"deviceId":   "dev-1",
		"salt":       []byte("salt-aaaa"),
		"nonce":      []byte("nonce-aaaaaa"),
		"ciphertext": []byte("sealed-state"),
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, first); rec.Code != http.StatusNoContent {
		t.Fatalf("first backup refused: got %d", rec.Code)
	}
}

// A refused upload must leave the stored backup completely intact — record AND blobs.
//
// The handler writes the new blobs to the blob store BEFORE it knows whether the record will be
// replaced, and deletes the previous ones after. A refusal that ran in the wrong order would
// either delete the blobs the surviving record still points at — turning a refusal into exactly
// the loss it was refusing — or leave the new ones behind as landfill.
func TestKeyBackupRefusalLeavesTheStoredBackupUsable(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "refusal@pheme.test")

	full := map[string]any{
		"deviceId":             "has-the-history",
		"salt":                 []byte("salt-aaaa"),
		"nonce":                []byte("nonce-aaaaaa"),
		"ciphertext":           []byte("sealed-state"),
		"transcriptSalt":       []byte("t-salt"),
		"transcriptNonce":      []byte("t-nonce"),
		"transcriptCiphertext": []byte("the whole history"),
		"transcriptMessages":   500,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, full); rec.Code != http.StatusNoContent {
		t.Fatalf("seed: got %d", rec.Code)
	}

	empty := map[string]any{
		"deviceId":   "fresh",
		"salt":       []byte("salt-bbbb"),
		"nonce":      []byte("nonce-bbbbbb"),
		"ciphertext": []byte("sealed-state-2"),
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, empty); rec.Code != http.StatusConflict {
		t.Fatalf("refusal: got %d, want 409", rec.Code)
	}

	// The whole backup must still be fetchable and openable — not merely present in the record.
	got := getBackup(t, f, token)
	if string(got.Ciphertext) != "sealed-state" {
		t.Fatalf("state blob lost or replaced by a refused upload: %q", got.Ciphertext)
	}
	if string(got.TranscriptCiphertext) != "the whole history" {
		t.Fatalf("transcript blob lost or replaced by a refused upload: %q", got.TranscriptCiphertext)
	}
}

// A transcript whose seal is present but whose salt or nonce is not can never be opened. Storing it
// would make a restore report a history it cannot deliver, which is worse than reporting none.
func TestKeyBackupRejectsAnUnopenableTranscript(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "partial@pheme.test")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"no salt", map[string]any{
			"transcriptNonce": []byte("t-nonce"), "transcriptCiphertext": []byte("sealed"),
		}},
		{"no nonce", map[string]any{
			"transcriptSalt": []byte("t-salt"), "transcriptCiphertext": []byte("sealed"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"deviceId":   "dev",
				"salt":       []byte("salt-aaaa"),
				"nonce":      []byte("nonce-aaaaaa"),
				"ciphertext": []byte("sealed-state"),
			}
			for k, v := range tc.body {
				body[k] = v
			}
			if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", rec.Code)
			}
		})
	}
}

// The count and the blob must describe the same backup. A count with no transcript behind it would
// raise the bar for every later upload against a history that is not stored — locking the account
// out of backing up at all.
func TestKeyBackupCountWithoutATranscriptDoesNotRaiseTheBar(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "phantom@pheme.test")

	// A count claimed with no transcript to back it.
	claimed := map[string]any{
		"deviceId":           "liar",
		"salt":               []byte("salt-aaaa"),
		"nonce":              []byte("nonce-aaaaaa"),
		"ciphertext":         []byte("sealed-state"),
		"transcriptMessages": 9000,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, claimed); rec.Code != http.StatusNoContent {
		t.Fatalf("seed: got %d", rec.Code)
	}

	// A later, honest backup carrying a real transcript must not be refused on account of it.
	honest := map[string]any{
		"deviceId":             "honest",
		"salt":                 []byte("salt-bbbb"),
		"nonce":                []byte("nonce-bbbbbb"),
		"ciphertext":           []byte("sealed-state-2"),
		"transcriptSalt":       []byte("t-salt"),
		"transcriptNonce":      []byte("t-nonce"),
		"transcriptCiphertext": []byte("a real history"),
		"transcriptMessages":   10,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, honest); rec.Code != http.StatusNoContent {
		t.Fatalf("an honest backup was refused because of a phantom count: got %d", rec.Code)
	}
}

// THE INCIDENT, REPLAYED. A device that had read everything backs up; a freshly installed one comes
// up empty and backs up over it. Before versioning that was the end of the history — the previous
// transcript's salt and nonce went with the record it was overwritten on, so even a surviving blob
// could never be opened. Now the superseded backup is archived and can be fetched whole.
func TestASupersededBackupIsStillRecoverable(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "superseded@pheme.test")

	full := map[string]any{
		"deviceId":             "had-the-history",
		"salt":                 []byte("salt-aaaa"),
		"nonce":                []byte("nonce-aaaaaa"),
		"ciphertext":           []byte("state-one"),
		"transcriptSalt":       []byte("t-salt-1"),
		"transcriptNonce":      []byte("t-nonce-1"),
		"transcriptCiphertext": []byte("a hundred messages"),
		"transcriptMessages":   100,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, full); rec.Code != http.StatusNoContent {
		t.Fatalf("seed: got %d", rec.Code)
	}

	// The fresh device replaces it — deliberately, as a person would after being warned.
	empty := map[string]any{
		"deviceId":             "freshly-installed",
		"salt":                 []byte("salt-bbbb"),
		"nonce":                []byte("nonce-bbbbbb"),
		"ciphertext":           []byte("state-two"),
		"transcriptSalt":       []byte("t-salt-2"),
		"transcriptNonce":      []byte("t-nonce-2"),
		"transcriptCiphertext": []byte("almost nothing"),
		"transcriptMessages":   1,
		"force":                true,
	}
	if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, empty); rec.Code != http.StatusNoContent {
		t.Fatalf("forced replace: got %d", rec.Code)
	}

	// The live backup is the poor one — that was the person's choice.
	if got := getBackup(t, f, token); string(got.TranscriptCiphertext) != "almost nothing" {
		t.Fatalf("live transcript = %q", got.TranscriptCiphertext)
	}

	// But the history is not gone. It is one fetch away.
	rec := f.do(http.MethodGet, "/v1/mls/key-backup/versions", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list versions: got %d", rec.Code)
	}
	var listed struct {
		Versions []struct {
			ID                 string `json:"id"`
			TranscriptMessages int    `json:"transcriptMessages"`
			HasTranscript      bool   `json:"hasTranscript"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode versions: %v", err)
	}
	if len(listed.Versions) != 1 {
		t.Fatalf("archived %d versions, want 1", len(listed.Versions))
	}
	if listed.Versions[0].TranscriptMessages != 100 || !listed.Versions[0].HasTranscript {
		t.Fatalf("the archived version does not describe the lost history: %+v", listed.Versions[0])
	}

	// And it opens: its OWN salt and nonce came with it, which is exactly what used to be lost.
	rec = f.do(http.MethodGet, "/v1/mls/key-backup/versions/"+listed.Versions[0].ID, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get version: got %d", rec.Code)
	}
	var version backupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &version); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if string(version.TranscriptCiphertext) != "a hundred messages" {
		t.Fatalf("archived transcript = %q, want the hundred messages", version.TranscriptCiphertext)
	}
	if string(version.TranscriptSalt) != "t-salt-1" || string(version.TranscriptNonce) != "t-nonce-1" {
		t.Fatal("the archived version lost its own salt/nonce, so it can never be opened")
	}
}

// The archive is bounded, or a chatty client grows it without limit. Each version costs a
// transcript, so the oldest are dropped and their blobs go with them.
func TestTheBackupArchiveIsBounded(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "bounded@pheme.test")

	for i := 0; i < keptBackupVersions+3; i++ {
		body := map[string]any{
			"deviceId":             fmt.Sprintf("dev-%d", i),
			"salt":                 []byte(fmt.Sprintf("salt-%d", i)),
			"nonce":                []byte(fmt.Sprintf("nonce-%d", i)),
			"ciphertext":           []byte(fmt.Sprintf("state-%d", i)),
			"transcriptSalt":       []byte(fmt.Sprintf("t-salt-%d", i)),
			"transcriptNonce":      []byte(fmt.Sprintf("t-nonce-%d", i)),
			"transcriptCiphertext": []byte(fmt.Sprintf("transcript-%d", i)),
			// Growing, so the shrink guard never fires and each upload really does replace.
			"transcriptMessages": i + 1,
		}
		if rec := f.do(http.MethodPut, "/v1/mls/key-backup", token, body); rec.Code != http.StatusNoContent {
			t.Fatalf("upload %d: got %d", i, rec.Code)
		}
	}

	rec := f.do(http.MethodGet, "/v1/mls/key-backup/versions", token, nil)
	var listed struct {
		Versions []struct {
			ID string `json:"id"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Versions) != keptBackupVersions {
		t.Fatalf("archive holds %d versions, want %d", len(listed.Versions), keptBackupVersions)
	}
}

// One person's archive is not another's, and a version id is not a capability.
func TestBackupVersionsAreScopedToTheirOwner(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice-v@pheme.test")
	_, bobTok := f.user(t, "bob-v@pheme.test")

	for i := 0; i < 2; i++ {
		body := map[string]any{
			"deviceId":             "dev",
			"salt":                 []byte("salt"),
			"nonce":                []byte("nonce"),
			"ciphertext":           []byte(fmt.Sprintf("alice-state-%d", i)),
			"transcriptSalt":       []byte("t-salt"),
			"transcriptNonce":      []byte("t-nonce"),
			"transcriptCiphertext": []byte("alice's history"),
			"transcriptMessages":   i + 1,
		}
		if rec := f.do(http.MethodPut, "/v1/mls/key-backup", aliceTok, body); rec.Code != http.StatusNoContent {
			t.Fatalf("alice upload %d: got %d", i, rec.Code)
		}
	}

	rec := f.do(http.MethodGet, "/v1/mls/key-backup/versions", aliceTok, nil)
	var listed struct {
		Versions []struct {
			ID string `json:"id"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed.Versions) == 0 {
		t.Fatalf("alice has no archived versions: %v", err)
	}

	// Bob sees none of his own, and cannot fetch one of Alice's by id.
	rec = f.do(http.MethodGet, "/v1/mls/key-backup/versions", bobTok, nil)
	var bobList struct {
		Versions []json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bobList); err != nil {
		t.Fatalf("decode bob: %v", err)
	}
	if len(bobList.Versions) != 0 {
		t.Fatalf("bob sees %d of alice's versions", len(bobList.Versions))
	}
	if rec := f.do(http.MethodGet, "/v1/mls/key-backup/versions/"+listed.Versions[0].ID, bobTok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob fetched alice's backup version: got %d, want 404", rec.Code)
	}
}
