package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func TestDeleteMessageRemovesCommentsAndBlobs(t *testing.T) {
	f := newAppFixture(t)
	token, user := f.tokenFor(t, "moderator@b.com")
	ch := createChannel(t, f, token, "Alerts")

	// A message with a comment hanging off it.
	msg := seedMessage(t, f, ch.ID, "doomed", "body", time.Now().UTC())
	if _, err := f.store.CreateComment(context.Background(), domain.Comment{
		MessageID: msg.ID, ChannelID: ch.ID, UserID: user.ID, Body: "hi",
	}); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/messages/"+msg.ID, token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body)
	}

	// Gone from the feed.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages", token, nil)
	var page struct {
		Messages []messageView `json:"messages"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	for _, m := range page.Messages {
		if m.ID == msg.ID {
			t.Fatalf("deleted message still listed")
		}
	}

	// Its comments went with it.
	counts, err := f.store.CommentCountsByMessages(context.Background(), []string{msg.ID})
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[msg.ID] != 0 {
		t.Errorf("comments outlived their message: %d", counts[msg.ID])
	}
}

// Only a moderator may delete; a plain member may not.
func TestDeleteMessageRejectsNonModerator(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, _ := f.tokenFor(t, "owner-del@b.com")
	ch := createChannel(t, f, ownerToken, "Alerts")
	msg := seedMessage(t, f, ch.ID, "safe", "body", time.Now().UTC())

	otherToken, _ := f.tokenFor(t, "other-del@b.com")
	rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/messages/"+msg.ID, otherToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// A moderator of one channel must not be able to delete another channel's message.
func TestDeleteMessageRejectsCrossChannel(t *testing.T) {
	f := newAppFixture(t)
	tokenA, _ := f.tokenFor(t, "a-del@b.com")
	chA := createChannel(t, f, tokenA, "A")

	tokenB, _ := f.tokenFor(t, "b-del@b.com")
	chB := createChannel(t, f, tokenB, "B")
	victim := seedMessage(t, f, chB.ID, "b's message", "body", time.Now().UTC())

	// A's owner names their own channel in the path but B's message in the id.
	rec := f.do(http.MethodDelete, "/v1/channels/"+chA.ID+"/messages/"+victim.ID, tokenA, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (message is not in that channel)", rec.Code)
	}
	if _, err := f.store.MessageByID(context.Background(), victim.ID); err != nil {
		t.Errorf("another channel's message was deleted: %v", err)
	}
}
