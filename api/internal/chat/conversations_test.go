package chat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func createDirect(t *testing.T, f *fixture, token, otherID string) domain.Conversation {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations", token, map[string]any{
		"kind": "direct", "memberIds": []string{otherID},
	})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create direct: status %d, body %s", rec.Code, rec.Body)
	}
	var c domain.Conversation
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	return c
}

// A direct chat is unique per pair: starting one twice, in either order, returns
// the same conversation rather than a second row.
func TestDirectChatIsDeduped(t *testing.T) {
	f := newFixture(t)
	alice, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")

	first := createDirect(t, f, aliceTok, bob)

	// Alice again → same conversation.
	again := createDirect(t, f, aliceTok, bob)
	if again.ID != first.ID {
		t.Fatalf("second create made a new conversation: %s vs %s", again.ID, first.ID)
	}
	// Bob, the other way round → still the same one.
	fromBob := createDirect(t, f, bobTok, alice)
	if fromBob.ID != first.ID {
		t.Fatalf("reversed pair made a new conversation: %s vs %s", fromBob.ID, first.ID)
	}
}

// Every conversation operation is gated on membership; a non-member gets 404
// (not 403), so a conversation's existence never leaks.
func TestNonMemberCannotSeeConversation(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, _ := f.user(t, "bob@b.com")
	_, malloryTok := f.user(t, "mallory@b.com")

	conv := createDirect(t, f, aliceTok, bob)

	for _, path := range []string{
		"/v1/conversations/" + conv.ID,
		"/v1/conversations/" + conv.ID + "/messages",
		"/v1/conversations/" + conv.ID + "/members",
	} {
		if rec := f.do(http.MethodGet, path, malloryTok, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s as non-member = %d, want 404", path, rec.Code)
		}
	}
	// And she cannot post into it.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv.ID+"/messages", malloryTok,
		map[string]any{"ciphertext": []byte("hi")})
	if rec.Code != http.StatusNotFound {
		t.Errorf("post as non-member = %d, want 404", rec.Code)
	}
}

// The server stores and returns message content verbatim without interpreting
// it — the property the whole E2EE design rests on.
func TestMessageContentIsOpaqueAndRoundTrips(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)

	blob := []byte{0x00, 0x01, 0x02, 0xff, 0xfe} // arbitrary bytes, not valid UTF-8
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv.ID+"/messages", aliceTok,
		map[string]any{"ciphertext": blob, "contentType": "application/mls"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post message: status %d, body %s", rec.Code, rec.Body)
	}

	// Bob reads it back byte-for-byte.
	rec = f.do(http.MethodGet, "/v1/conversations/"+conv.ID+"/messages", bobTok, nil)
	var page struct {
		Messages []domain.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(page.Messages))
	}
	got := page.Messages[0]
	if string(got.Ciphertext) != string(blob) {
		t.Errorf("ciphertext round-trip changed the bytes: %v vs %v", got.Ciphertext, blob)
	}
	if got.SenderID != memberID(t, f, conv.ID, "alice@b.com") {
		// Sender is attributed (unlike a channel Message).
		t.Errorf("message not attributed to its sender")
	}
}

// Group admin controls membership; a plain member cannot add or remove others,
// but can remove themselves (leave).
func TestGroupMembershipAuthority(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "admin@b.com")
	member, memberTok := f.user(t, "member@b.com")
	outsider, _ := f.user(t, "outsider@b.com")

	rec := f.do(http.MethodPost, "/v1/conversations", adminTok, map[string]any{
		"kind": "group", "title": "Team", "memberIds": []string{member},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rec.Code, rec.Body)
	}
	var group domain.Conversation
	_ = json.Unmarshal(rec.Body.Bytes(), &group)

	// A plain member cannot add someone.
	rec = f.do(http.MethodPost, "/v1/conversations/"+group.ID+"/members", memberTok,
		map[string]any{"userId": outsider})
	if rec.Code != http.StatusForbidden {
		t.Errorf("member adding = %d, want 403", rec.Code)
	}
	// The admin can.
	rec = f.do(http.MethodPost, "/v1/conversations/"+group.ID+"/members", adminTok,
		map[string]any{"userId": outsider})
	if rec.Code != http.StatusCreated {
		t.Errorf("admin adding = %d, want 201; body %s", rec.Code, rec.Body)
	}
	// A member can remove themselves (leave).
	rec = f.do(http.MethodDelete, "/v1/conversations/"+group.ID+"/members/"+member, memberTok, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("self-leave = %d, want 204", rec.Code)
	}
}

func memberID(t *testing.T, f *fixture, convID, email string) string {
	t.Helper()
	u, err := f.store.UserByEmail(nil, email)
	if err != nil {
		// Fall back: look up via members list is overkill; UserByEmail on Memory
		// ignores ctx, so nil is fine here.
		t.Fatalf("user lookup: %v", err)
	}
	return u.ID
}
