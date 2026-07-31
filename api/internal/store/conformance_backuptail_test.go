package store

import (
	"context"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// The append-only backup tail, against BOTH implementations.
//
// The tail is the only copy of a message body between the moment it is decrypted and the next
// snapshot, so every rule here is a rule about whether a history survives. It runs against Mongo
// as well as memory on purpose: the sibling PutKeyBackup persists an explicit field list, and a
// field added to the struct without being added there was written nowhere — a backup that looked
// healthy and held nothing. A conformance test is what catches that shape of mistake.
func TestKeyBackupTail(t *testing.T) {
	entry := func(conversation, message, body string) domain.MLSKeyBackupTailEntry {
		return domain.MLSKeyBackupTailEntry{
			UserID:         "u1",
			DeviceID:       "d1",
			ConversationID: conversation,
			MessageID:      message,
			Salt:           []byte("salt"),
			Nonce:          []byte("nonce"),
			Ciphertext:     []byte(body),
			CreatedAt:      time.Now().UTC(),
		}
	}

	runNamed(t, "an appended body comes back whole", func(t *testing.T, sut storeUnderTest) {
		if err := sut.store.AppendKeyBackupTail(context.Background(), []domain.MLSKeyBackupTailEntry{
			entry("c1", "m1", "sealed-one"),
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
		got, err := sut.store.ListKeyBackupTail(context.Background(), "u1")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1 entry, got %d", len(got))
		}
		// Every field, because a tail entry that loses its salt or nonce is a body that can never
		// be opened again — indistinguishable, later, from one that was never backed up.
		if got[0].ConversationID != "c1" || got[0].MessageID != "m1" {
			t.Errorf("entry lost its identity: %+v", got[0])
		}
		if string(got[0].Ciphertext) != "sealed-one" {
			t.Errorf("ciphertext = %q", got[0].Ciphertext)
		}
		if string(got[0].Salt) != "salt" || string(got[0].Nonce) != "nonce" {
			t.Errorf("entry lost its salt or nonce: %+v", got[0])
		}
	})

	// The property that lets a client retry an append it never got an answer to. Without it, the
	// only safe client is one that never retries — which means a body lost to a dropped
	// connection is lost for good.
	runNamed(t, "appending the same body twice stores it once", func(t *testing.T, sut storeUnderTest) {
		for i := 0; i < 3; i++ {
			if err := sut.store.AppendKeyBackupTail(context.Background(), []domain.MLSKeyBackupTailEntry{
				entry("c1", "m1", "sealed-one"),
			}); err != nil {
				t.Fatalf("append %d: %v", i, err)
			}
		}
		got, _ := sut.store.ListKeyBackupTail(context.Background(), "u1")
		if len(got) != 1 {
			t.Fatalf("want 1 entry after three identical appends, got %d", len(got))
		}
		n, err := sut.store.CountKeyBackupTail(context.Background(), "u1")
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Errorf("count = %d, want 1 — a duplicate inflates the shrink guard", n)
		}
	})

	runNamed(t, "one user's tail is invisible to another", func(t *testing.T, sut storeUnderTest) {
		mine := entry("c1", "m1", "mine")
		theirs := entry("c1", "m1", "theirs")
		theirs.UserID = "u2"
		if err := sut.store.AppendKeyBackupTail(context.Background(), []domain.MLSKeyBackupTailEntry{mine, theirs}); err != nil {
			t.Fatalf("append: %v", err)
		}
		got, _ := sut.store.ListKeyBackupTail(context.Background(), "u1")
		if len(got) != 1 || string(got[0].Ciphertext) != "mine" {
			t.Fatalf("u1 sees %d entries: %+v", len(got), got)
		}
	})

	// Truncation is the one destructive operation here, and it runs on every checkpoint. An
	// off-by-one takes bodies the snapshot does not contain.
	runNamed(t, "truncating drops what the snapshot absorbed and keeps the rest", func(t *testing.T, sut storeUnderTest) {
		old := entry("c1", "m1", "inside the snapshot")
		old.CreatedAt = time.Now().UTC().Add(-time.Hour)
		boundary := entry("c1", "m2", "written as the snapshot began")
		cut := time.Now().UTC().Add(-time.Minute)
		boundary.CreatedAt = cut
		fresh := entry("c1", "m3", "written during the upload")
		fresh.CreatedAt = time.Now().UTC()

		if err := sut.store.AppendKeyBackupTail(context.Background(), []domain.MLSKeyBackupTailEntry{old, boundary, fresh}); err != nil {
			t.Fatalf("append: %v", err)
		}
		removed, err := sut.store.TruncateKeyBackupTail(context.Background(), "u1", cut)
		if err != nil {
			t.Fatalf("truncate: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed %d, want 1", removed)
		}

		got, _ := sut.store.ListKeyBackupTail(context.Background(), "u1")
		if len(got) != 2 {
			t.Fatalf("want 2 surviving entries, got %d", len(got))
		}
		// The boundary entry stays. It was stamped at the instant the snapshot began and may not
		// be inside it; keeping a duplicate costs nothing, dropping a body cannot be undone.
		kept := map[string]bool{}
		for _, e := range got {
			kept[e.MessageID] = true
		}
		if !kept["m2"] || !kept["m3"] {
			t.Errorf("truncate took a body the snapshot may not hold: kept %v", kept)
		}
	})

	runNamed(t, "an empty append is not an error", func(t *testing.T, sut storeUnderTest) {
		if err := sut.store.AppendKeyBackupTail(context.Background(), nil); err != nil {
			t.Fatalf("append of nothing: %v", err)
		}
		n, _ := sut.store.CountKeyBackupTail(context.Background(), "u1")
		if n != 0 {
			t.Errorf("count = %d, want 0", n)
		}
	})

	// Replay order. A restore merges oldest-to-newest so that where a body was appended twice the
	// later one is seen last.
	runNamed(t, "entries come back oldest first", func(t *testing.T, sut storeUnderTest) {
		first := entry("c1", "m1", "first")
		first.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
		second := entry("c1", "m2", "second")
		second.CreatedAt = time.Now().UTC().Add(-time.Hour)
		third := entry("c1", "m3", "third")

		if err := sut.store.AppendKeyBackupTail(context.Background(), []domain.MLSKeyBackupTailEntry{third, first, second}); err != nil {
			t.Fatalf("append: %v", err)
		}
		got, _ := sut.store.ListKeyBackupTail(context.Background(), "u1")
		if len(got) != 3 {
			t.Fatalf("want 3, got %d", len(got))
		}
		for i, want := range []string{"m1", "m2", "m3"} {
			if got[i].MessageID != want {
				t.Fatalf("position %d = %s, want %s (order: %+v)", i, got[i].MessageID, want, got)
			}
		}
	})
}

// runNamed keeps each rule a named subtest while still running against every implementation.
func runNamed(t *testing.T, name string, fn func(t *testing.T, sut storeUnderTest)) {
	t.Helper()
	t.Run(name, func(t *testing.T) { eachStore(t, fn) })
}
