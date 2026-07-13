package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// seedMessage writes a message directly to the store, bypassing the broker so
// the test does not depend on the dispatcher.
func seedMessage(t *testing.T, f *appFixture, channelID, title, body string, at time.Time) domain.Message {
	t.Helper()
	m, err := f.store.CreateMessage(context.Background(), domain.Message{
		ChannelID:       channelID,
		Title:           title,
		Body:            body,
		CommentsAllowed: true,
		CreatedAt:       at,
	})
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return m
}

func createChannel(t *testing.T, f *appFixture, token, name string) domain.Channel {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/channels", token,
		map[string]any{"name": name, "subscriptionMode": "open"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel: status %d, body %s", rec.Code, rec.Body)
	}
	var ch domain.Channel
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	return ch
}

// The chat list renders a preview line per channel, so the channel list must
// carry each channel's newest message — and omit it for a channel with none.
func TestListChannelsCarriesLastMessage(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	withMessages := createChannel(t, f, token, "Alerts")
	empty := createChannel(t, f, token, "Quiet")

	now := time.Now().UTC()
	seedMessage(t, f, withMessages.ID, "older", "older body", now.Add(-time.Hour))
	newest := seedMessage(t, f, withMessages.ID, "newest", "newest body", now)

	rec := f.do(http.MethodGet, "/v1/channels", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rec.Code, rec.Body)
	}
	var list struct {
		Channels []channelView `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}

	byID := make(map[string]channelView, len(list.Channels))
	for _, c := range list.Channels {
		byID[c.ID] = c
	}

	got := byID[withMessages.ID]
	if got.LastMessage == nil {
		t.Fatalf("channel with messages has no lastMessage")
	}
	if got.LastMessage.ID != newest.ID {
		t.Errorf("lastMessage.id = %q, want the newest message %q", got.LastMessage.ID, newest.ID)
	}
	if got.LastMessage.Title != "newest" {
		t.Errorf("lastMessage.title = %q, want %q", got.LastMessage.Title, "newest")
	}
	if lm := byID[empty.ID].LastMessage; lm != nil {
		t.Errorf("channel without messages has lastMessage = %+v, want nil", lm)
	}
}

// A body longer than previewChars would otherwise ship in full to the chat list.
func TestListChannelsTruncatesPreviewBody(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	ch := createChannel(t, f, token, "Verbose")
	seedMessage(t, f, ch.ID, "t", strings.Repeat("x", previewChars+50), time.Now().UTC())

	rec := f.do(http.MethodGet, "/v1/channels", token, nil)
	var list struct {
		Channels []channelView `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Channels) != 1 || list.Channels[0].LastMessage == nil {
		t.Fatalf("unexpected list: %+v", list)
	}
	if got := len(list.Channels[0].LastMessage.Body); got != previewChars {
		t.Errorf("preview body length = %d, want %d", got, previewChars)
	}
}

// previewBody must not split a multi-byte rune.
func TestPreviewBodyRuneSafe(t *testing.T) {
	long := strings.Repeat("я", previewChars+10)
	got := previewBody(long)
	if !utf8.ValidString(got) {
		t.Fatalf("preview body is not valid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != previewChars {
		t.Errorf("preview body = %d runes, want %d", n, previewChars)
	}
}

// The feed labels its comment button from commentCount, so listMessages and
// getMessage must both carry it.
func TestMessagesCarryCommentCount(t *testing.T) {
	f := newAppFixture(t)
	token, user := f.tokenFor(t, "owner@b.com")
	ch := createChannel(t, f, token, "Alerts")
	commented := seedMessage(t, f, ch.ID, "with comments", "body", time.Now().UTC())
	bare := seedMessage(t, f, ch.ID, "no comments", "body", time.Now().UTC().Add(-time.Minute))

	for range 2 {
		if _, err := f.store.CreateComment(context.Background(), domain.Comment{
			MessageID: commented.ID,
			ChannelID: ch.ID,
			UserID:    user.ID,
			Body:      "hi",
		}); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
	}

	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages status = %d, body %s", rec.Code, rec.Body)
	}
	var page struct {
		Messages []messageView `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	counts := make(map[string]int64, len(page.Messages))
	for _, m := range page.Messages {
		counts[m.ID] = m.CommentCount
	}
	if counts[commented.ID] != 2 {
		t.Errorf("commentCount for commented message = %d, want 2", counts[commented.ID])
	}
	if counts[bare.ID] != 0 {
		t.Errorf("commentCount for bare message = %d, want 0", counts[bare.ID])
	}

	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages/"+commented.ID, token, nil)
	var single messageView
	if err := json.Unmarshal(rec.Body.Bytes(), &single); err != nil {
		t.Fatalf("decode single: %v", err)
	}
	if single.CommentCount != 2 {
		t.Errorf("getMessage commentCount = %d, want 2", single.CommentCount)
	}
}
