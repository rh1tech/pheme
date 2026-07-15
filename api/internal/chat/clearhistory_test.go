package chat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func messageCount(t *testing.T, f *fixture, convID, token string) int {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/conversations/"+convID+"/messages", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages: status %d, body %s", rec.Code, rec.Body)
	}
	var page struct {
		Messages []domain.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return len(page.Messages)
}

func postCiphertext(t *testing.T, f *fixture, convID, token string, body []byte) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+convID+"/messages", token,
		map[string]any{"ciphertext": body, "contentType": "application/mls"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post message: status %d, body %s", rec.Code, rec.Body)
	}
}

// Clearing history is per-member: it hides the caller's own view of the messages so
// far — on every device, since the server enforces it — while leaving the shared log
// and every other member's view untouched. The conversation itself stays, and messages
// sent after the clear are visible again.
func TestClearHistoryIsPerMember(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)

	postCiphertext(t, f, conv.ID, aliceTok, []byte("one"))
	postCiphertext(t, f, conv.ID, bobTok, []byte("two"))

	if n := messageCount(t, f, conv.ID, aliceTok); n != 2 {
		t.Fatalf("before clear, alice sees %d messages, want 2", n)
	}

	// Alice clears her history.
	rec := f.do(http.MethodDelete, "/v1/conversations/"+conv.ID+"/messages", aliceTok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear history: status %d, body %s", rec.Code, rec.Body)
	}

	// Alice's history is empty; Bob still sees both — the log was not touched.
	if n := messageCount(t, f, conv.ID, aliceTok); n != 0 {
		t.Errorf("after clear, alice sees %d messages, want 0", n)
	}
	if n := messageCount(t, f, conv.ID, bobTok); n != 2 {
		t.Errorf("after alice clears, bob sees %d messages, want 2 (his view is untouched)", n)
	}

	// The conversation itself survives, and new messages appear past the watermark.
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv.ID, aliceTok, nil); rec.Code != http.StatusOK {
		t.Errorf("conversation gone after clear: status %d", rec.Code)
	}
	postCiphertext(t, f, conv.ID, bobTok, []byte("three"))
	if n := messageCount(t, f, conv.ID, aliceTok); n != 1 {
		t.Errorf("after clear, alice sees %d messages, want 1 (only the new one)", n)
	}
	if n := messageCount(t, f, conv.ID, bobTok); n != 3 {
		t.Errorf("bob sees %d messages, want 3", n)
	}
}

// A non-member cannot clear a conversation's history — the operation is gated on
// membership like every other conversation op, and answers 404 so existence never leaks.
func TestNonMemberCannotClearHistory(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, _ := f.user(t, "bob@b.com")
	_, malloryTok := f.user(t, "mallory@b.com")

	conv := createDirect(t, f, aliceTok, bob)
	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv.ID+"/messages", malloryTok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("clear as non-member = %d, want 404", rec.Code)
	}
}
