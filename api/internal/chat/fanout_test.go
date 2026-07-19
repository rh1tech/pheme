package chat

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/rh1tech/pheme/api/internal/live"
)

// Who a live event names.
//
// A chat message event must carry the list of members entitled to it. That list is what the SSE
// loop checks in memory instead of asking the database once per open connection — the difference
// between a cost proportional to the conversation and one proportional to the whole logged-in
// population.
//
// This is easy to lose by accident: publishing without the list still WORKS, because the SSE loop
// falls back to a per-connection lookup. Nothing breaks, nothing errors, the server just quietly
// becomes quadratic again. Measured at a thousand streams, that fallback meant three-and-a-half
// second delivery and a fifth of messages dropped. Hence a test.

// captureBus records what was published.
type captureBus struct {
	live.Bus
	events []live.Event
}

func (c *captureBus) Publish(e live.Event) {
	c.events = append(c.events, e)
	c.Bus.Publish(e)
}

func TestAMessageEventNamesEveryMemberSoTheStreamNeedNotAsk(t *testing.T) {
	f := newFixture(t)
	bus := &captureBus{Bus: live.NewMemoryBus()}
	f.handler.Live = bus

	owner, ownerToken := f.user(t, "owner@pheme.test")
	alice, _ := f.user(t, "alice@pheme.test")
	bob, _ := f.user(t, "bob@pheme.test")

	conv := f.group(t, ownerToken, "the group", alice, bob)

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", ownerToken,
		map[string]any{"ciphertext": []byte("hello")})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post = %d: %s", rec.Code, rec.Body)
	}

	var event *live.Event
	for i := range bus.events {
		if bus.events[i].ChatMessage != nil {
			event = &bus.events[i]
		}
	}
	if event == nil {
		t.Fatal("no chat message event was published")
	}

	if len(event.Recipients) == 0 {
		t.Fatal("the event named nobody. The SSE loop will fall back to a database lookup per open " +
			"connection, which is the quadratic behaviour this list exists to avoid.")
	}

	got := append([]string(nil), event.Recipients...)
	want := []string{owner, alice, bob}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("recipients = %v, want the three members %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recipients = %v, want %v", got, want)
		}
	}
}

// A member removed from the conversation must not appear in the next event's list. The list is an
// authorisation decision, so a stale one would keep delivering a group's messages to someone who
// was thrown out of it.
func TestARemovedMemberIsNoLongerNamed(t *testing.T) {
	f := newFixture(t)
	bus := &captureBus{Bus: live.NewMemoryBus()}
	f.handler.Live = bus

	owner, ownerToken := f.user(t, "owner2@pheme.test")
	gone, _ := f.user(t, "gone@pheme.test")
	conv := f.group(t, ownerToken, "shrinking", gone)

	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv+"/members/"+gone, ownerToken, nil); rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("remove member = %d: %s", rec.Code, rec.Body)
	}

	bus.events = nil
	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", ownerToken,
		map[string]any{"ciphertext": []byte("after")}); rec.Code != http.StatusCreated {
		t.Fatalf("post = %d: %s", rec.Code, rec.Body)
	}

	for _, e := range bus.events {
		if e.ChatMessage == nil {
			continue
		}
		for _, id := range e.Recipients {
			if id == gone {
				t.Fatalf("a removed member is still named on the event; their open stream would keep "+
					"receiving this group's messages (recipients=%v)", e.Recipients)
			}
		}
		if len(e.Recipients) == 0 {
			t.Fatal("the event named nobody after a removal")
		}
		found := false
		for _, id := range e.Recipients {
			if id == owner {
				found = true
			}
		}
		if !found {
			t.Errorf("the owner is missing from %v", e.Recipients)
		}
	}
}

// group creates a group conversation and returns its id.
func (f *fixture) group(t *testing.T, ownerToken, name string, memberIDs ...string) string {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations", ownerToken, map[string]any{
		"kind": "group", "name": name, "memberIds": memberIDs,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if out.ID == "" {
		t.Fatalf("created conversation has no id: %s", rec.Body)
	}
	// The membership rows are written by the same request, so nothing to wait for — but assert it,
	// because a test that raced would fail confusingly later.
	if _, err := f.store.ConversationMembers(t.Context(), out.ID); err != nil {
		t.Fatalf("members: %v", err)
	}
	return out.ID
}
