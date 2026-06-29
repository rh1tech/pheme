package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// seedMessage persists a message directly into a channel's history so read-path
// access control can be exercised without going through the dispatcher.
func (f *appFixture) seedMessage(t *testing.T, channelID, title, body string) domain.Message {
	t.Helper()
	msg, err := f.store.CreateMessage(context.Background(), domain.Message{
		ChannelID: channelID,
		Title:     title,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return msg
}

// listedMessages decodes the message-list response into a slice.
func listedMessages(t *testing.T, body []byte) []domain.Message {
	t.Helper()
	var out struct {
		Messages []domain.Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return out.Messages
}

// canSeeMessage reports whether the response disclosed the given message,
// regardless of whether it came back via the list or single-message shape.
func containsMessage(msgs []domain.Message, id string) bool {
	for _, m := range msgs {
		if m.ID == id {
			return true
		}
	}
	return false
}

// Requirement 1: a user who subscribes to an approval channel but has not yet
// been approved (membership pending) must not see any of the channel's messages,
// neither in the history list nor by direct message id.
func TestPendingSubscriberCannotSeeMessages(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, _ := f.tokenFor(t, "sub@b.com")

	ch := f.createChannelMode(t, owner, "Approval", domain.ModeApproval)
	msg := f.seedMessage(t, ch.ID, "Secret", "members only")

	// Subscriber joins → membership is pending (awaiting approval).
	rec := f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	var jr struct {
		Membership domain.ChannelMember `json:"membership"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &jr)
	if jr.Membership.Status != domain.MemberPending {
		t.Fatalf("membership status = %q, want pending", jr.Membership.Status)
	}

	// History list must not disclose the message to a pending member.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages", subTok, nil)
	if rec.Code == http.StatusOK {
		if msgs := listedMessages(t, rec.Body.Bytes()); containsMessage(msgs, msg.ID) {
			t.Fatalf("pending member saw message in history list (status 200, %d messages); want access denied", len(msgs))
		}
	} else if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("list messages status = %d, want 200-with-none, 403 or 404", rec.Code)
	}

	// Direct message id must not disclose the message either.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages/"+msg.ID, subTok, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("pending member read message by id (status 200); want access denied. body=%s", rec.Body)
	}
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("get message status = %d, want 403 or 404", rec.Code)
	}
}

// Requirement 2: when an active member is banned (membership blocked), they must
// lose read access immediately — the very next request must not disclose any
// messages, even ones that were visible to them moments before.
func TestBannedUserLosesAccessImmediately(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, subUser := f.tokenFor(t, "sub@b.com")

	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)
	msg := f.seedMessage(t, ch.ID, "Hello", "world")

	// Join an open channel → active immediately, and the member can read history.
	if rec := f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID}); rec.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages", subTok, nil)
	if rec.Code != http.StatusOK || !containsMessage(listedMessages(t, rec.Body.Bytes()), msg.ID) {
		t.Fatalf("active member could not read message before ban: status=%d body=%s", rec.Code, rec.Body)
	}

	// Owner bans the member.
	if rec := f.do(http.MethodPatch, "/v1/channels/"+ch.ID+"/members/"+subUser.ID, owner,
		map[string]any{"status": "blocked"}); rec.Code != http.StatusOK {
		t.Fatalf("ban: %d %s", rec.Code, rec.Body)
	}

	// Immediately after the ban, the history list must no longer disclose messages.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages", subTok, nil)
	if rec.Code == http.StatusOK {
		if msgs := listedMessages(t, rec.Body.Bytes()); containsMessage(msgs, msg.ID) {
			t.Fatalf("banned user still saw message in history list; want access revoked immediately")
		}
	} else if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("list messages after ban status = %d, want 200-with-none, 403 or 404", rec.Code)
	}

	// And direct-by-id access must be revoked too.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages/"+msg.ID, subTok, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("banned user read message by id (status 200); want access revoked. body=%s", rec.Body)
	}
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("get message after ban status = %d, want 403 or 404", rec.Code)
	}
}
