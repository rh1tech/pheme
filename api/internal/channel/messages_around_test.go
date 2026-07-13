package channel

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// A search hit has to be readable in context, so the window must carry messages
// from both sides of it — not just the older ones a cursor would give.
func TestMessagesAroundReturnsBothSides(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "around@b.com")
	ch := createChannel(t, f, token, "Alerts")

	base := time.Now().UTC().Add(-time.Hour)
	ids := make([]string, 0, 21)
	for i := range 21 {
		m := seedMessage(t, f, ch.ID, "", "message", base.Add(time.Duration(i)*time.Minute))
		ids = append(ids, m.ID)
	}
	centre := ids[10] // the middle one

	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/messages?around="+centre+"&limit=11", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var page struct {
		Messages []messageView `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var newer, older int
	found := false
	for _, m := range page.Messages {
		switch {
		case m.ID == centre:
			found = true
		case found:
			older++
		default:
			newer++
		}
	}
	if !found {
		t.Fatalf("window does not contain the message it was centred on")
	}
	if newer == 0 {
		t.Errorf("window has nothing newer than the centre — a cursor would already do that")
	}
	if older == 0 {
		t.Errorf("window has nothing older than the centre")
	}

	// Newest-first, like every other message list.
	for i := 1; i < len(page.Messages); i++ {
		if page.Messages[i].CreatedAt.After(page.Messages[i-1].CreatedAt) {
			t.Fatalf("window is not newest-first at %d", i)
		}
	}
}

// A message id from another channel must not open a window into it.
func TestMessagesAroundRejectsForeignMessage(t *testing.T) {
	f := newAppFixture(t)
	tokenA, _ := f.tokenFor(t, "a-around@b.com")
	chA := createChannel(t, f, tokenA, "A")

	tokenB, _ := f.tokenFor(t, "b-around@b.com")
	chB := createChannel(t, f, tokenB, "B")
	foreign := seedMessage(t, f, chB.ID, "secret", "body", time.Now().UTC())

	rec := f.do(http.MethodGet, "/v1/channels/"+chA.ID+"/messages?around="+foreign.ID, tokenA, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
