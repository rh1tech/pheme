package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Which conversation a history handoff belongs to.
//
// The handoff is a sealed transcript one device leaves for another that is joining. The fetch is
// one-shot: reading it DELETES it, so the blob exists only until the joining device collects it.
//
// That makes the authorisation question sharper than it looks. requireMember proves the caller
// belongs to the conversation in the URL — it says nothing about where the blob came from. Without a
// binding, a member of ANY conversation who learned an id could not merely read another
// conversation's handoff, but destroy it: the rightful device then finds nothing waiting and joins
// with no history at all.
//
// Attachments already solve this, with a stored record and an explicit ConversationID check, and
// say so in a comment. History had no record and no check.

// uploadHistoryTo posts a handoff blob and returns the id the server hands back.
func uploadHistoryTo(t *testing.T, f *fixture, token, conv string, body string) string {
	t.Helper()
	rec := f.doRaw(http.MethodPost, "/v1/conversations/"+conv+"/mls/history", token, []byte(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload history = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ID == "" {
		t.Fatal("upload returned no id")
	}
	return out.ID
}

func TestHistoryHandoffRoundTripsAndIsConsumedOnce(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "hist-owner@pheme.test")
	other, _ := f.user(t, "hist-other@pheme.test")
	conv := f.group(t, ownerToken, "handoff", other)

	id := uploadHistoryTo(t, f, ownerToken, conv, "sealed transcript")

	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history/"+id, ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch = %d: %s", rec.Code, rec.Body)
	}
	if rec.Body.String() != "sealed transcript" {
		t.Errorf("got %q, want the sealed bytes back unchanged", rec.Body.String())
	}

	// One-shot: the handoff is consumed, so blobs do not accumulate.
	if again := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history/"+id, ownerToken, nil); again.Code != http.StatusNotFound {
		t.Errorf("second fetch = %d, want 404 — the handoff should be consumed", again.Code)
	}
}

// THE ONE THAT MATTERS. Another conversation's handoff must not be reachable — and above all must
// not be destroyed by the attempt.
func TestAHandoffCannotBeFetchedFromAnotherConversation(t *testing.T) {
	f := newFixture(t)
	_, victimToken := f.user(t, "hist-victim@pheme.test")
	victimPeer, _ := f.user(t, "hist-victim-peer@pheme.test")
	victimConv := f.group(t, victimToken, "theirs", victimPeer)

	_, attackerToken := f.user(t, "hist-attacker@pheme.test")
	attackerPeer, _ := f.user(t, "hist-attacker-peer@pheme.test")
	attackerConv := f.group(t, attackerToken, "mine", attackerPeer)

	id := uploadHistoryTo(t, f, victimToken, victimConv, "the victims transcript")

	// The attacker is a legitimate member of their OWN conversation, which is all requireMember
	// ever proved. They present the other conversation's id.
	rec := f.do(http.MethodGet, "/v1/conversations/"+attackerConv+"/mls/history/"+id, attackerToken, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("a member of another conversation fetched this handoff: %q", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-conversation fetch = %d, want 404", rec.Code)
	}

	// And crucially it is still there. The fetch deletes on success, so a failed attempt that
	// deleted anyway would let anyone destroy a handoff they could not read.
	ok := f.do(http.MethodGet, "/v1/conversations/"+victimConv+"/mls/history/"+id, victimToken, nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("the rightful device can no longer fetch its handoff (%d); the failed attempt "+
			"destroyed it, and the joining device gets no history at all", ok.Code)
	}
	if ok.Body.String() != "the victims transcript" {
		t.Errorf("the handoff came back as %q", ok.Body.String())
	}
}

// The id names its conversation. Asserted directly, because the binding is the whole mechanism —
// if it silently stopped being applied, every test above would still pass on the happy path.
func TestAHistoryIDNamesItsConversation(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "hist-bound@pheme.test")
	other, _ := f.user(t, "hist-bound2@pheme.test")
	conv := f.group(t, ownerToken, "bound", other)

	id := uploadHistoryTo(t, f, ownerToken, conv, "bytes")
	if !strings.HasPrefix(id, conv+".") {
		t.Errorf("history id %q does not name its conversation %q; the fetch has nothing to check "+
			"the blob against", id, conv)
	}
}

// An id from before the binding existed no longer resolves. Deliberate and self-healing: a handoff
// is one-shot and short-lived, and a failed fetch produces a fresh offer, which is the same path a
// retry has always taken.
func TestAnUnboundHistoryIDIsRefused(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "hist-legacy@pheme.test")
	other, _ := f.user(t, "hist-legacy2@pheme.test")
	conv := f.group(t, ownerToken, "legacy", other)

	id := uploadHistoryTo(t, f, ownerToken, conv, "bytes")
	bare := strings.TrimPrefix(id, conv+".")

	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history/"+bare, ownerToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("an unbound id = %d, want 404", rec.Code)
	}
	// And refusing it did not consume the blob.
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history/"+id, ownerToken, nil); rec.Code != http.StatusOK {
		t.Errorf("refusing an unbound id destroyed the handoff (%d)", rec.Code)
	}
}

// Outsiders are refused by membership, before any of this matters.
func TestHistoryIsMembersOnly(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "hist-priv@pheme.test")
	other, _ := f.user(t, "hist-priv2@pheme.test")
	_, outsiderToken := f.user(t, "hist-outsider@pheme.test")
	conv := f.group(t, ownerToken, "private handoff", other)

	id := uploadHistoryTo(t, f, ownerToken, conv, "sealed")

	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history/"+id, outsiderToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("an outsider fetched the handoff (%d)", rec.Code)
	}
	if rec := f.doRaw(http.MethodPost, "/v1/conversations/"+conv+"/mls/history", outsiderToken, []byte("theirs")); rec.Code != http.StatusNotFound {
		t.Errorf("an outsider uploaded a handoff into someone else's conversation (%d)", rec.Code)
	}
	// Still collectable by the rightful device.
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history/"+id, ownerToken, nil); rec.Code != http.StatusOK {
		t.Errorf("the outsider's attempt cost the rightful device its handoff (%d)", rec.Code)
	}
}

// doRaw posts an unencoded body. The history endpoint takes raw sealed bytes rather than JSON, so
// the ordinary fixture helper (which marshals) cannot express it.
func (f *fixture) doRaw(method, path, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}
