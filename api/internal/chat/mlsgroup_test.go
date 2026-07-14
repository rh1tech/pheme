package chat

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The state a client reads to decide what to do: establish the group, join it, or
// commit against it.
type groupState struct {
	GroupID       string   `json:"groupId"`
	Epoch         int64    `json:"epoch"`
	PriorGroupIDs []string `json:"priorGroupIds"`
}

func mlsState(t *testing.T, f *fixture, token, conv string) groupState {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get mls state: got %d (%s)", rec.Code, rec.Body.String())
	}
	var out groupState
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode mls state: %v", err)
	}
	return out
}

// commit posts a Commit based on baseEpoch and returns the status plus the state the
// server came back with (the new one on success, the CURRENT one on conflict).
func commit(t *testing.T, f *fixture, token, conv, groupID string, baseEpoch int64) (int, groupState) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", token, map[string]any{
		"groupId":   groupID,
		"baseEpoch": baseEpoch,
		"welcome":   []byte("opaque-welcome"),
		"commit":    []byte("opaque-commit"),
	})
	var out groupState
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A conversation has no MLS group until somebody establishes one, and it is
// established exactly once. Two devices racing to set up the same conversation — which
// is precisely what happens when one person opens a new chat on their phone and their
// laptop — must not both win, or they encrypt into two different groups under the same
// name and neither can read the other.
func TestGroupIsEstablishedExactlyOnce(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-g@pheme.test")
	bobID, _ := f.user(t, "bob-g@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if got := mlsState(t, f, aliceToken, conv); got.GroupID != "" || got.Epoch != 0 {
		t.Fatalf("a fresh conversation must have no group, got %+v", got)
	}

	// Alice's laptop establishes the group.
	if code, state := commit(t, f, aliceToken, conv, "grp-laptop", 0); code != http.StatusOK {
		t.Fatalf("establish: got %d", code)
	} else if state.GroupID != "grp-laptop" || state.Epoch != 1 {
		t.Fatalf("after establish: %+v, want grp-laptop at epoch 1", state)
	}

	// Alice's phone, which knew nothing about it, tries to establish its own. It must
	// be refused — and told which group actually won, so it can join that one instead
	// of building a second conversation nobody else is in.
	code, state := commit(t, f, aliceToken, conv, "grp-phone", 0)
	if code != http.StatusConflict {
		t.Fatalf("a second establish: got %d, want 409", code)
	}
	if state.GroupID != "grp-laptop" || state.Epoch != 1 {
		t.Fatalf("the conflict must report the winning group, got %+v", state)
	}

	// And the group really is untouched.
	if got := mlsState(t, f, aliceToken, conv); got.GroupID != "grp-laptop" || got.Epoch != 1 {
		t.Fatalf("group after the losing establish: %+v", got)
	}
}

// Two members Commit against the same epoch. Exactly one can win: an MLS group has one
// history, and a member who applies the loser's Commit is forked off the conversation
// for good. The loser is told where the group actually is so it can catch up and retry.
func TestConcurrentCommitsAreSerialised(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-h@pheme.test")
	bobID, bobToken := f.user(t, "bob-h@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := commit(t, f, aliceToken, conv, "grp-1", 0); code != http.StatusOK {
		t.Fatalf("establish: got %d", code)
	}

	// Both members build a Commit against epoch 1, neither having seen the other's.
	if code, state := commit(t, f, aliceToken, conv, "grp-1", 1); code != http.StatusOK {
		t.Fatalf("alice's commit: got %d", code)
	} else if state.Epoch != 2 {
		t.Fatalf("alice's commit left the group at epoch %d, want 2", state.Epoch)
	}

	code, state := commit(t, f, bobToken, conv, "grp-1", 1)
	if code != http.StatusConflict {
		t.Fatalf("bob's stale commit: got %d, want 409", code)
	}
	if state.Epoch != 2 {
		t.Fatalf("the conflict must report the epoch to catch up to, got %d", state.Epoch)
	}

	// Bob catches up and re-proposes against the epoch that actually happened.
	if code, state := commit(t, f, bobToken, conv, "grp-1", 2); code != http.StatusOK {
		t.Fatalf("bob's retry: got %d", code)
	} else if state.Epoch != 3 {
		t.Fatalf("bob's retry left the group at epoch %d, want 3", state.Epoch)
	}
}

// A Commit is only relayed if the server accepted it. A refused Commit must leave no
// trace in the log — above all no Welcome, since a device that joined from a Welcome
// belonging to a rejected Commit would land in a group with nobody else in it.
func TestARejectedCommitRelaysNothing(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-i@pheme.test")
	bobID, bobToken := f.user(t, "bob-i@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := commit(t, f, aliceToken, conv, "grp-1", 0); code != http.StatusOK {
		t.Fatalf("establish: got %d", code)
	}
	before := len(listMessages(t, f, aliceToken, conv))

	if code, _ := commit(t, f, bobToken, conv, "grp-1", 0); code != http.StatusConflict {
		t.Fatalf("a commit on a stale epoch: got %d, want 409", code)
	}

	if after := len(listMessages(t, f, aliceToken, conv)); after != before {
		t.Fatalf("a rejected commit put %d message(s) in the log; it must relay nothing", after-before)
	}
}

// The roster's rule — "only a group admin can remove members" — must also hold for the
// Commits that change the encrypted group, or the two disagree about who may throw
// somebody out and a plain member can quietly cut anyone off from the conversation.
//
// The check is on what the client DECLARES it is removing, because the Commit itself is
// opaque and the server cannot read it (see mlsCommitRequest.Removes). So this pins the
// honest path, not the cryptography — that distinction is real and is documented there.
func TestOnlyAnAdminMayRemoveFromTheEncryptedGroup(t *testing.T) {
	f := newFixture(t)
	ownerID, ownerToken := f.user(t, "owner-rm@pheme.test")
	bobID, bobToken := f.user(t, "bob-rm@pheme.test")
	carolID, _ := f.user(t, "carol-rm@pheme.test")
	group := f.createGroup(t, ownerToken, []string{bobID, carolID})

	if code, _ := commit(t, f, ownerToken, group, "grp-1", 0); code != http.StatusOK {
		t.Fatalf("establish: got %d", code)
	}

	// Bob is an ordinary member. He may not remove Carol.
	rec := f.do(http.MethodPost, "/v1/conversations/"+group+"/mls/commit", bobToken, map[string]any{
		"groupId":   "grp-1",
		"baseEpoch": 1,
		"commit":    []byte("opaque-commit"),
		"removes":   []string{carolID},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a non-admin removing another member: got %d, want 403", rec.Code)
	}

	// He may prune his OWN leaves — a ghost device of his own is his business.
	rec = f.do(http.MethodPost, "/v1/conversations/"+group+"/mls/commit", bobToken, map[string]any{
		"groupId":   "grp-1",
		"baseEpoch": 1,
		"commit":    []byte("opaque-commit"),
		"removes":   []string{bobID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("a member pruning their own leaves: got %d (%s)", rec.Code, rec.Body.String())
	}

	// And the admin may remove anyone.
	rec = f.do(http.MethodPost, "/v1/conversations/"+group+"/mls/commit", ownerToken, map[string]any{
		"groupId":   "grp-1",
		"baseEpoch": 2,
		"commit":    []byte("opaque-commit"),
		"removes":   []string{carolID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("an admin removing a member: got %d (%s)", rec.Code, rec.Body.String())
	}
	_ = ownerID
}

// A direct chat has no admin, and the only removals in one are ghost-device pruning —
// so either party may make them.
func TestEitherPartyMayPruneInADirectChat(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-dp@pheme.test")
	bobID, bobToken := f.user(t, "bob-dp@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := commit(t, f, aliceToken, conv, "grp-1", 0); code != http.StatusOK {
		t.Fatalf("establish: got %d", code)
	}
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", bobToken, map[string]any{
		"groupId":   "grp-1",
		"baseEpoch": 1,
		"commit":    []byte("opaque-commit"),
		"removes":   []string{bobID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("pruning in a direct chat: got %d (%s)", rec.Code, rec.Body.String())
	}
}

// A group nobody holds any more can be retired, so the conversation can start a fresh one.
//
// This is the only way out of a real dead end. Every device that held the group can lose its
// key material — a browser cleared, an iOS PWA evicted on the seven-day rule — and admission is
// a Commit, which only a member of the group can make. Once nobody holds it, nobody can let
// anybody in, and the conversation is dead forever.
//
// The retired group is REMEMBERED. That is what makes this safe to do without proof, and it is
// the whole difference between this and the old "rebuild the group" behaviour: that one deleted
// the group and took every message in the conversation with it. Anyone who still holds an old
// group can still read every message that was sent to it.
func TestAGroupNobodyHoldsCanBeRetiredAndReplaced(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-reset@pheme.test")
	bobID, bobToken := f.user(t, "bob-reset@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := commit(t, f, aliceToken, conv, "grp-dead", 0); code != http.StatusOK {
		t.Fatalf("establish: got %d", code)
	}
	if code, _ := commit(t, f, aliceToken, conv, "grp-dead", 1); code != http.StatusOK {
		t.Fatalf("advance: got %d", code)
	}

	// Bob's device has announced itself and given up: nobody is coming.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/reset", bobToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: got %d (%s)", rec.Code, rec.Body.String())
	}

	// The conversation now has no group — so one can be established — and it REMEMBERS the old
	// one, which is what stops this destroying the history that was encrypted to it.
	after := mlsState(t, f, bobToken, conv)
	if after.GroupID != "" || after.Epoch != 0 {
		t.Fatalf("after a reset the conversation must have no group, got %+v", after)
	}
	if len(after.PriorGroupIDs) != 1 || after.PriorGroupIDs[0] != "grp-dead" {
		t.Fatalf("the retired group must be remembered, got %v — anything encrypted to it "+
			"would otherwise become unreadable for everyone who can still read it today",
			after.PriorGroupIDs)
	}

	// And a fresh group establishes cleanly on top.
	if code, state := commit(t, f, bobToken, conv, "grp-new", 0); code != http.StatusOK {
		t.Fatalf("establish after reset: got %d", code)
	} else if state.GroupID != "grp-new" || state.Epoch != 1 {
		t.Fatalf("after re-establish: %+v", state)
	}
}

// Two members who both give up at the same moment must not retire two groups between them.
func TestConcurrentResetsRetireOneGroup(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-r2@pheme.test")
	bobID, bobToken := f.user(t, "bob-r2@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if code, _ := commit(t, f, aliceToken, conv, "grp-1", 0); code != http.StatusOK {
		t.Fatalf("establish: got %d", code)
	}

	for _, tok := range []string{aliceToken, bobToken} {
		if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/reset", tok, nil); rec.Code != http.StatusOK {
			t.Fatalf("reset: got %d", rec.Code)
		}
	}

	// The second reset found nothing established and did nothing.
	after := mlsState(t, f, bobToken, conv)
	if len(after.PriorGroupIDs) != 1 {
		t.Fatalf("retired %v; a second reset must not retire an empty group", after.PriorGroupIDs)
	}
}

// Only a member may retire a conversation's group.
func TestResetRequiresMembership(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-r3@pheme.test")
	bobID, _ := f.user(t, "bob-r3@pheme.test")
	_, outsiderToken := f.user(t, "outsider-r3@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/reset", outsiderToken, nil); rec.Code == http.StatusOK {
		t.Fatal("an outsider must not be able to retire a conversation's group")
	}
}

// Only a member may read or advance a conversation's group.
func TestMLSGroupRequiresMembership(t *testing.T) {
	f := newFixture(t)
	_, aliceToken := f.user(t, "alice-j@pheme.test")
	bobID, _ := f.user(t, "bob-j@pheme.test")
	_, outsiderToken := f.user(t, "outsider@pheme.test")
	conv := f.createDirect(t, aliceToken, bobID)

	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls", outsiderToken, nil); rec.Code == http.StatusOK {
		t.Fatal("an outsider must not read the conversation's group state")
	}
	if code, _ := commit(t, f, outsiderToken, conv, "grp-x", 0); code == http.StatusOK {
		t.Fatal("an outsider must not be able to establish the group")
	}
}

func listMessages(t *testing.T, f *fixture, token, conv string) []struct {
	ID          string `json:"id"`
	ContentType string `json:"contentType"`
} {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/messages", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages: got %d", rec.Code)
	}
	var out struct {
		Messages []struct {
			ID          string `json:"id"`
			ContentType string `json:"contentType"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return out.Messages
}
