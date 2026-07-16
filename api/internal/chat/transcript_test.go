package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func postTyped(t *testing.T, f *fixture, convID, token, contentType string, body []byte) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+convID+"/messages", token,
		map[string]any{"ciphertext": body, "contentType": contentType})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post %s: status %d, body %s", contentType, rec.Code, rec.Body)
	}
}

func transcript(t *testing.T, f *fixture, convID, token, query string) []domain.ChatMessage {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/conversations/"+convID+"/messages"+query, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages: status %d, body %s", rec.Code, rec.Body)
	}
	var page struct {
		Messages []domain.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return page.Messages
}

// The feed is a transcript, not the raw log. MLS protocol traffic rides the same ordered log
// but nobody wrote it and no client renders it, so it must not come back here — and, above all,
// must not count against the page limit. It used to: a chat whose recent log was mostly device
// announcements and Commits returned a page of 50 that drew as a handful of messages.
func TestTranscriptExcludesMLSProtocolTraffic(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)

	postTyped(t, f, conv.ID, aliceTok, "application/mls", []byte("one"))
	postTyped(t, f, conv.ID, bobTok, domain.ContentTypeMLSDevice, []byte("d1"))
	postTyped(t, f, conv.ID, aliceTok, "application/mls", []byte("two"))
	postTyped(t, f, conv.ID, bobTok, domain.ContentTypeMLSDevice, []byte("d2"))

	msgs := transcript(t, f, conv.ID, aliceTok, "")
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want 2 (the protocol traffic must not be in it)", len(msgs))
	}
	for _, m := range msgs {
		if domain.IsMLSProtocol(m.ContentType) {
			t.Errorf("protocol traffic in the transcript: %s", m.ContentType)
		}
	}
}

// A conversation's lastMessage is the last thing SAID. Letting protocol traffic take that slot
// lit an unread dot with nothing behind it — and a permanent one: opening the chat marks read up
// to the newest real message, which is older than the announcement, so it never cleared.
func TestLastMessageIsNeverProtocolTraffic(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)

	postTyped(t, f, conv.ID, aliceTok, "application/mls", []byte("the real last word"))
	// Bob's device announces itself afterwards — newer than anything anyone said.
	postTyped(t, f, conv.ID, bobTok, domain.ContentTypeMLSDevice, []byte("announce"))

	rec := f.do(http.MethodGet, "/v1/conversations", aliceTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list conversations: status %d, body %s", rec.Code, rec.Body)
	}
	var page struct {
		Conversations []struct {
			ID          string              `json:"id"`
			LastMessage *domain.ChatMessage `json:"lastMessage"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found bool
	for _, c := range page.Conversations {
		if c.ID != conv.ID {
			continue
		}
		found = true
		if c.LastMessage == nil {
			t.Fatal("conversation has no lastMessage, want the real one")
		}
		if domain.IsMLSProtocol(c.LastMessage.ContentType) {
			t.Errorf("lastMessage is protocol traffic (%s) — this is the phantom unread dot",
				c.LastMessage.ContentType)
		}
		if string(c.LastMessage.Ciphertext) != "the real last word" {
			t.Errorf("lastMessage = %q, want the last real message", c.LastMessage.Ciphertext)
		}
	}
	if !found {
		t.Fatal("conversation missing from the list")
	}
}

// A page of `limit` is `limit` things people actually said. Protocol traffic interleaved with
// them must not eat slots — that is the whole point of filtering it in the store rather than
// in the client, where it had already been counted.
func TestTranscriptPageIsFullOfRealMessages(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)

	// Ten real messages, each followed by a device announcement.
	for i := 0; i < 10; i++ {
		postTyped(t, f, conv.ID, aliceTok, "application/mls", []byte(fmt.Sprintf("m%d", i)))
		postTyped(t, f, conv.ID, bobTok, domain.ContentTypeMLSDevice, []byte("noise"))
	}

	msgs := transcript(t, f, conv.ID, aliceTok, "?limit=5")
	if len(msgs) != 5 {
		t.Fatalf("page returned %d messages, want a full 5 — protocol traffic is eating the page", len(msgs))
	}
	for _, m := range msgs {
		if domain.IsMLSProtocol(m.ContentType) {
			t.Errorf("protocol traffic in the page: %s", m.ContentType)
		}
	}
}
