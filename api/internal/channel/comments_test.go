package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// seedMessageComments persists a message with an explicit comments-allowed flag.
func (f *appFixture) seedMessageComments(t *testing.T, channelID, title string, allowed bool) domain.Message {
	t.Helper()
	msg, err := f.store.CreateMessage(context.Background(), domain.Message{
		ChannelID:       channelID,
		Title:           title,
		CommentsAllowed: allowed,
		CreatedAt:       time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return msg
}

// joinOpen joins an open channel (membership becomes active immediately).
func (f *appFixture) joinOpen(t *testing.T, token string, ch domain.Channel) {
	t.Helper()
	if rec := f.do(http.MethodPost, "/v1/channels/join", token, map[string]any{"ref": ch.PublicID}); rec.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
}

func commentsPath(ch domain.Channel, msg domain.Message) string {
	return "/v1/channels/" + ch.ID + "/messages/" + msg.ID + "/comments"
}

func TestPostAndListComment(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	member, memberUser := f.tokenFor(t, "member@b.com")

	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)
	msg := f.seedMessageComments(t, ch.ID, "Hello", true)
	f.joinOpen(t, member, ch)

	// Set a username so the author projection carries it.
	f.do(http.MethodPatch, "/v1/me", member, map[string]any{"username": "member1", "displayName": "Mem"})

	rec := f.do(http.MethodPost, commentsPath(ch, msg), member, map[string]any{"body": "nice post"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post comment status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var created commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Body != "nice post" || created.Author.ID != memberUser.ID || created.Author.Username != "member1" {
		t.Fatalf("unexpected comment: %+v", created)
	}

	rec = f.do(http.MethodGet, commentsPath(ch, msg), member, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	// Author projection must not leak the email.
	if raw := rec.Body.String(); strings.Contains(raw, "member@b.com") {
		t.Fatalf("comment list leaked author email: %s", raw)
	}
	var list struct {
		Comments []commentView `json:"comments"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Comments) != 1 || list.Comments[0].ID != created.ID {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestPostCommentRejectedForNonMember(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	stranger, _ := f.tokenFor(t, "stranger@b.com")

	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)
	msg := f.seedMessageComments(t, ch.ID, "Hello", true)

	rec := f.do(http.MethodPost, commentsPath(ch, msg), stranger, map[string]any{"body": "hi"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}

func TestPostCommentRejectedWhenClosed(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	member, _ := f.tokenFor(t, "member@b.com")

	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)
	msg := f.seedMessageComments(t, ch.ID, "NoComments", false)
	f.joinOpen(t, member, ch)

	rec := f.do(http.MethodPost, commentsPath(ch, msg), member, map[string]any{"body": "hi"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}

func TestDeleteCommentAuthorAndOwnerAndStranger(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	member, _ := f.tokenFor(t, "member@b.com")
	other, _ := f.tokenFor(t, "other@b.com")

	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)
	msg := f.seedMessageComments(t, ch.ID, "Hello", true)
	f.joinOpen(t, member, ch)
	f.joinOpen(t, other, ch)

	post := func(token, body string) commentView {
		rec := f.do(http.MethodPost, commentsPath(ch, msg), token, map[string]any{"body": body})
		if rec.Code != http.StatusCreated {
			t.Fatalf("post status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		var c commentView
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return c
	}
	del := func(token, id string) int {
		return f.do(http.MethodDelete, commentsPath(ch, msg)+"/"+id, token, nil).Code
	}

	// Another member cannot delete someone else's comment.
	c1 := post(member, "mine")
	if code := del(other, c1.ID); code != http.StatusForbidden {
		t.Fatalf("stranger delete status = %d, want 403", code)
	}
	// The author can delete their own.
	if code := del(member, c1.ID); code != http.StatusNoContent {
		t.Fatalf("author delete status = %d, want 204", code)
	}
	// The channel owner can delete any comment.
	c2 := post(other, "theirs")
	if code := del(owner, c2.ID); code != http.StatusNoContent {
		t.Fatalf("owner delete status = %d, want 204", code)
	}
}

// TestNotifyCommentsAllowedThreading verifies the per-message flag flows through
// the notify pipeline: absent ⇒ true (default on), explicit false ⇒ false.
func TestNotifyCommentsAllowedThreading(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	ch := f.createChannelMode(t, owner, "Send", domain.ModeOpen)

	// Absent field defaults to comments allowed.
	if rec := f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", owner, map[string]any{"title": "a"}); rec.Code != http.StatusAccepted {
		t.Fatalf("notify status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	if task := f.drainTask(t); !task.CommentsAllowed {
		t.Fatalf("absent commentsAllowed should default true, got false")
	}

	// Explicit false disables comments.
	if rec := f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", owner, map[string]any{"title": "b", "commentsAllowed": false}); rec.Code != http.StatusAccepted {
		t.Fatalf("notify status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	if task := f.drainTask(t); task.CommentsAllowed {
		t.Fatalf("explicit commentsAllowed=false should be false, got true")
	}
}
