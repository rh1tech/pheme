package chat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// createGroup makes a group owned by `token`'s user with the given members and
// returns its id.
func (f *fixture) createGroup(t *testing.T, token string, memberIDs []string) string {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations", token, map[string]any{
		"kind": "group", "title": "Test", "memberIds": memberIDs,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: got %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.ID
}

// A group's roles and lifecycle are the owner/admin's to control, and a plain member
// cannot escalate: they cannot change roles, add or remove others, or delete the group.
func TestGroupAdminControls(t *testing.T) {
	f := newFixture(t)
	ownerID, ownerTok := f.user(t, "owner-g@pheme.test")
	bobID, bobTok := f.user(t, "bob-g@pheme.test")
	carolID, _ := f.user(t, "carol-g@pheme.test")

	conv := f.createGroup(t, ownerTok, []string{bobID})

	// Bob (a plain member) cannot change roles, add, or delete.
	if rec := f.do(http.MethodPatch, "/v1/conversations/"+conv+"/members/"+bobID, bobTok, map[string]any{"role": "admin"}); rec.Code != http.StatusForbidden {
		t.Fatalf("member self-promote: expected 403, got %d", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/members", bobTok, map[string]any{"userId": carolID}); rec.Code != http.StatusForbidden {
		t.Fatalf("member add: expected 403, got %d", rec.Code)
	}
	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv, bobTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member delete group: expected 403, got %d", rec.Code)
	}

	// The owner promotes Bob; now Bob may add Carol.
	if rec := f.do(http.MethodPatch, "/v1/conversations/"+conv+"/members/"+bobID, ownerTok, map[string]any{"role": "admin"}); rec.Code != http.StatusNoContent {
		t.Fatalf("owner promote bob: got %d", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/members", bobTok, map[string]any{"userId": carolID}); rec.Code != http.StatusCreated {
		t.Fatalf("promoted bob add carol: got %d (%s)", rec.Code, rec.Body.String())
	}

	// The owner deletes the group; it is then gone for everyone.
	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv, ownerTok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete group: got %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv, ownerTok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted group should be gone, got %d", rec.Code)
	}
	_ = ownerID
}

// Either party can delete a direct chat, and it disappears for both.
func TestDeleteDirectChat(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice-d@pheme.test")
	bobID, bobTok := f.user(t, "bob-d@pheme.test")

	conv := f.createDirect(t, aliceTok, bobID)
	// Bob (not the creator) can still delete it.
	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv, bobTok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete direct: got %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv, aliceTok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted direct chat should be gone for the other party, got %d", rec.Code)
	}
}

// A non-member cannot delete a conversation (404 — its existence is not even leaked).
func TestNonMemberCannotDelete(t *testing.T) {
	f := newFixture(t)
	_, aliceTok := f.user(t, "alice-nm@pheme.test")
	bobID, _ := f.user(t, "bob-nm@pheme.test")
	_, malloryTok := f.user(t, "mallory-nm@pheme.test")

	conv := f.createDirect(t, aliceTok, bobID)
	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv, malloryTok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("outsider delete: expected 404, got %d", rec.Code)
	}
}
