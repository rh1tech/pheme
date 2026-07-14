package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

// attachFixture is newFixture plus a blob store, which the base fixture leaves nil.
func attachFixture(t *testing.T) *fixture {
	t.Helper()
	blobs := blob.NewMemory()
	db := store.NewMemory(blobs)
	f := newFixture(t)
	f.store = db
	f.handler.Store = db
	f.handler.Blobs = blobs
	f.handler.Live = live.NewMemoryBus()
	return f
}

// raw posts a body that is not JSON — an attachment is opaque bytes, not an envelope.
func (f *fixture) raw(method, path, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) directChat(t *testing.T, token, otherID string) string {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations", token, map[string]any{
		"kind":      "direct",
		"memberIds": []string{otherID},
	})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create conversation: %d %s", rec.Code, rec.Body)
	}
	var conv domain.Conversation
	if err := json.Unmarshal(rec.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	return conv.ID
}

func upload(t *testing.T, f *fixture, convID, token string, data []byte) string {
	t.Helper()
	rec := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/attachments", token, data)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func TestAttachment_RoundTripsCiphertextUntouched(t *testing.T) {
	f := attachFixture(t)
	aID, aTok := f.user(t, "a@example.com")
	bID, bTok := f.user(t, "b@example.com")
	_ = aID
	convID := f.directChat(t, aTok, bID)

	// The server never sees a photo — only whatever ciphertext the sender hands it. So the one thing
	// it must do is give back exactly what it was given, byte for byte. A single mangled byte and the
	// GCM tag fails and the photo is gone for good.
	sealed := []byte{0x00, 0xff, 0x10, 0x80, 0x7f, 0x00, 0x01}
	id := upload(t, f, convID, aTok, sealed)

	rec := f.raw(http.MethodGet, "/v1/conversations/"+convID+"/attachments/"+id, bTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if !bytes.Equal(rec.Body.Bytes(), sealed) {
		t.Errorf("bytes changed in transit: got %x, want %x", rec.Body.Bytes(), sealed)
	}

	// And it must not claim to know what it is. The real content type is a property of the plaintext
	// and travels inside the encrypted message; a server that guessed one here would be guessing about
	// something it is not supposed to be able to see.
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
}

// The one that actually matters. Membership of the conversation in the URL is NOT enough: without
// checking that the blob belongs to that conversation, any member of any chat could read any
// attachment in the system by guessing — or simply by being told — an id.
func TestAttachment_CannotBeFetchedFromAnotherConversation(t *testing.T) {
	f := attachFixture(t)
	_, aTok := f.user(t, "a@example.com")
	bID, bTok := f.user(t, "b@example.com")
	cID, _ := f.user(t, "c@example.com")

	// A photo in A's chat with B.
	private := f.directChat(t, aTok, bID)
	id := upload(t, f, private, aTok, []byte("sealed photo"))

	// B is also in a chat with C. B asks for A's attachment id through THAT conversation, where B is
	// a perfectly legitimate member.
	other := f.directChat(t, bTok, cID)

	rec := f.raw(http.MethodGet, "/v1/conversations/"+other+"/attachments/"+id, bTok, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a member of another conversation fetched the attachment: %d %s", rec.Code, rec.Body)
	}
}

func TestAttachment_NonMemberIsRefused(t *testing.T) {
	f := attachFixture(t)
	_, aTok := f.user(t, "a@example.com")
	bID, _ := f.user(t, "b@example.com")
	_, strangerTok := f.user(t, "stranger@example.com")

	convID := f.directChat(t, aTok, bID)
	id := upload(t, f, convID, aTok, []byte("sealed photo"))

	rec := f.raw(http.MethodGet, "/v1/conversations/"+convID+"/attachments/"+id, strangerTok, nil)
	if rec.Code == http.StatusOK {
		t.Fatal("a stranger fetched an attachment from a conversation they are not in")
	}

	up := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/attachments", strangerTok, []byte("x"))
	if up.Code == http.StatusCreated {
		t.Fatal("a stranger uploaded into a conversation they are not in")
	}
}

func TestAttachment_TooLargeIsRefused(t *testing.T) {
	f := attachFixture(t)
	_, aTok := f.user(t, "a@example.com")
	bID, _ := f.user(t, "b@example.com")
	convID := f.directChat(t, aTok, bID)

	// A member must not be able to use a conversation as free object storage.
	oversize := bytes.Repeat([]byte{0xab}, maxAttachmentBytes+1)
	rec := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/attachments", aTok, oversize)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload = %d, want 413", rec.Code)
	}
}

// A photo whose key went with the deleted messages is landfill — nobody, including us, can ever open
// it again. It must not sit in the blob store forever.
func TestAttachment_DeletedWithTheConversation(t *testing.T) {
	f := attachFixture(t)
	_, aTok := f.user(t, "a@example.com")
	bID, _ := f.user(t, "b@example.com")
	convID := f.directChat(t, aTok, bID)

	id := upload(t, f, convID, aTok, []byte("sealed photo"))

	rec := f.do(http.MethodDelete, "/v1/conversations/"+convID, aTok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete conversation: %d %s", rec.Code, rec.Body)
	}

	if _, _, err := f.handler.Blobs.Get(context.Background(), id); err == nil {
		t.Error("the blob outlived the conversation whose key could open it")
	}
	if _, err := f.store.GetAttachment(context.Background(), id); err == nil {
		t.Error("the attachment record outlived the conversation")
	}
}

func TestAttachment_UnavailableWithoutABlobStore(t *testing.T) {
	// A deployment with no blob store configured must refuse rather than pretend.
	f := newFixture(t) // no Blobs
	_, aTok := f.user(t, "a@example.com")
	bID, _ := f.user(t, "b@example.com")
	convID := f.directChat(t, aTok, bID)

	rec := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/attachments", aTok, []byte("x"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload with no blob store = %d, want 503", rec.Code)
	}
}

func TestAttachment_EmptyIsRefused(t *testing.T) {
	f := attachFixture(t)
	_, aTok := f.user(t, "a@example.com")
	bID, _ := f.user(t, "b@example.com")
	convID := f.directChat(t, aTok, bID)

	rec := f.raw(http.MethodPost, "/v1/conversations/"+convID+"/attachments", aTok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty upload = %d, want 400", rec.Code)
	}
}

// The blob store is keyed on an unguessable id, but nothing stops a client sending a path that is
// not one. It must 404 rather than 500.
func TestAttachment_UnknownIdIs404(t *testing.T) {
	f := attachFixture(t)
	_, aTok := f.user(t, "a@example.com")
	bID, _ := f.user(t, "b@example.com")
	convID := f.directChat(t, aTok, bID)

	rec := f.raw(http.MethodGet,
		"/v1/conversations/"+convID+"/attachments/"+strings.Repeat("0", 32), aTok, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown attachment = %d, want 404", rec.Code)
	}
}
