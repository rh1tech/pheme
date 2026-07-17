package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// The history handoff stores a sealed blob a member uploads and hands it, verbatim and exactly
// once, to another member — then it is gone. The server never interprets the bytes.
func TestHistoryHandoffRoundTripIsOneShot(t *testing.T) {
	f := newFixture(t) // the base fixture now carries a blob store
	aID, aTok := f.user(t, "alice-h@pheme.test")
	bID, bTok := f.user(t, "bob-h@pheme.test")
	_ = aID
	convID := f.directChat(t, aTok, bID)

	sealed := []byte("sealed-transcript-bytes-the-server-cannot-read")
	rec := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/mls/history", aTok, sealed)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload history: got %d", rec.Code)
	}
	var up struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil || up.ID == "" {
		t.Fatalf("decode upload response: %v (%s)", err, rec.Body.String())
	}

	// Bob fetches it and gets the exact bytes.
	got := f.raw(http.MethodGet, "/v1/conversations/"+convID+"/mls/history/"+up.ID, bTok, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("fetch history: got %d", got.Code)
	}
	if !bytes.Equal(got.Body.Bytes(), sealed) {
		t.Fatalf("history round-trip mismatch: %q", got.Body.Bytes())
	}

	// One-shot: a second fetch finds nothing (the handoff was consumed).
	again := f.raw(http.MethodGet, "/v1/conversations/"+convID+"/mls/history/"+up.ID, bTok, nil)
	if again.Code != http.StatusNotFound {
		t.Fatalf("second fetch should 404, got %d", again.Code)
	}
}

// Only conversation members may offer or fetch history.
func TestHistoryHandoffIsMembersOnly(t *testing.T) {
	f := newFixture(t)
	aID, aTok := f.user(t, "alice-hm@pheme.test")
	bID, _ := f.user(t, "bob-hm@pheme.test")
	_, cTok := f.user(t, "carol-hm@pheme.test")
	_ = aID
	convID := f.directChat(t, aTok, bID)

	// Carol is not in the conversation.
	rec := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/mls/history", cTok, []byte("x"))
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("non-member upload should be refused, got %d", rec.Code)
	}
}

func TestHistoryHandoffRejectsEmpty(t *testing.T) {
	f := newFixture(t)
	aID, aTok := f.user(t, "alice-he@pheme.test")
	bID, _ := f.user(t, "bob-he@pheme.test")
	_ = aID
	convID := f.directChat(t, aTok, bID)

	rec := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/mls/history", aTok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty history should 400, got %d", rec.Code)
	}
}
