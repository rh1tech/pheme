package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func report(
	t *testing.T,
	f *fixture,
	convID, token string,
	body map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(http.MethodPost, "/v1/conversations/"+convID+"/receipts", token, body)
}

// memberOf returns a conversation member as the API hands it to a client — the shape the
// clients compute their ticks from.
func memberOf(t *testing.T, f *fixture, convID, token, userID string) domain.ConversationMember {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/conversations/"+convID, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get conversation: status %d, body %s", rec.Code, rec.Body)
	}
	var view struct {
		Members []domain.ConversationMember `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range view.Members {
		if m.UserID == userID {
			return m
		}
	}
	t.Fatalf("user %s is not a member", userID)
	return domain.ConversationMember{}
}

// A receipt says how far a member has got, and the answer only ever moves forward. Reports
// arrive out of order all the time — two devices, a retry, a catch-up after being offline —
// and an older one must never un-read what was read.
func TestReceiptsOnlyEverMoveForward(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)
	bobID := memberID(t, f, conv.ID, "bob@b.com")

	later := time.Now().UTC()
	earlier := later.Add(-1 * time.Hour)

	if rec := report(t, f, conv.ID, bobTok, map[string]any{"read": later}); rec.Code != http.StatusOK {
		t.Fatalf("report read: status %d, body %s", rec.Code, rec.Body)
	}
	// An older report lands afterwards — it must not drag the watermark back.
	if rec := report(t, f, conv.ID, bobTok, map[string]any{"read": earlier}); rec.Code != http.StatusOK {
		t.Fatalf("late report: status %d, body %s", rec.Code, rec.Body)
	}

	mem := memberOf(t, f, conv.ID, aliceTok, bobID)
	if !mem.ReadAt.Equal(later) {
		t.Errorf("readAt = %v, want it to have stayed at %v — an out-of-order report moved it back",
			mem.ReadAt, later)
	}
}

// You cannot read what never arrived. A client reporting only `read` must not leave a message
// double-ticked but not single-ticked.
func TestReadCarriesDeliveredWithIt(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)
	bobID := memberID(t, f, conv.ID, "bob@b.com")

	at := time.Now().UTC()
	if rec := report(t, f, conv.ID, bobTok, map[string]any{"read": at}); rec.Code != http.StatusOK {
		t.Fatalf("report read: status %d, body %s", rec.Code, rec.Body)
	}

	mem := memberOf(t, f, conv.ID, aliceTok, bobID)
	if !mem.ReadAt.Equal(at) {
		t.Errorf("readAt = %v, want %v", mem.ReadAt, at)
	}
	if mem.DeliveredAt.Before(at) {
		t.Errorf("deliveredAt = %v, want at least %v: a read message is necessarily delivered",
			mem.DeliveredAt, at)
	}
}

// Delivered on its own must NOT imply read — that is the whole difference between one tick
// and two.
func TestDeliveredDoesNotImplyRead(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)
	bobID := memberID(t, f, conv.ID, "bob@b.com")

	at := time.Now().UTC()
	if rec := report(t, f, conv.ID, bobTok, map[string]any{"delivered": at}); rec.Code != http.StatusOK {
		t.Fatalf("report delivered: status %d, body %s", rec.Code, rec.Body)
	}

	mem := memberOf(t, f, conv.ID, aliceTok, bobID)
	if !mem.DeliveredAt.Equal(at) {
		t.Errorf("deliveredAt = %v, want %v", mem.DeliveredAt, at)
	}
	if mem.ReadAt.After(mem.JoinedAt) {
		t.Errorf("readAt = %v, want it left at the join floor: delivered is not read", mem.ReadAt)
	}
}

// A member reports their own position and nobody else's, and only in a conversation they are in.
func TestNonMemberCannotReportReceipt(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, _ := f.user(t, "bob@b.com")
	_, malloryTok := f.user(t, "mallory@b.com")
	conv := createDirect(t, f, aliceTok, bob)

	rec := report(t, f, conv.ID, malloryTok, map[string]any{"read": time.Now().UTC()})
	if rec.Code != http.StatusNotFound {
		t.Errorf("report as non-member = %d, want 404", rec.Code)
	}
}

func TestReceiptRequiresSomething(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, bobTok := f.user(t, "bob@b.com")
	conv := createDirect(t, f, aliceTok, bob)

	if rec := report(t, f, conv.ID, bobTok, map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty report = %d, want 400", rec.Code)
	}
}

// A member starts caught up as of the moment they joined. Otherwise someone joining a group
// today would hold the ticks back on everything said before they arrived — messages MLS gives
// them no way to read, so the wait could never end.
func TestNewMemberStartsCaughtUpAtJoin(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice@b.com")
	bob, _ := f.user(t, "bob@b.com")
	carol, _ := f.user(t, "carol@b.com")

	groupID := f.createGroup(t, aliceTok, []string{bob})
	postCiphertext(t, f, groupID, aliceTok, []byte("said before carol arrived"))

	rec := f.do(http.MethodPost, "/v1/conversations/"+groupID+"/members", aliceTok,
		map[string]any{"userId": carol})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add member: status %d, body %s", rec.Code, rec.Body)
	}

	mem := memberOf(t, f, groupID, aliceTok, carol)
	if mem.JoinedAt.IsZero() {
		t.Fatal("member has no joinedAt")
	}
	if !mem.DeliveredAt.Equal(mem.JoinedAt) || !mem.ReadAt.Equal(mem.JoinedAt) {
		t.Errorf("new member watermarks = delivered %v / read %v, want both at joinedAt %v — "+
			"otherwise they hold back the ticks on everything said before they arrived",
			mem.DeliveredAt, mem.ReadAt, mem.JoinedAt)
	}
}
