package chat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Group chats, end to end: who may change the roster, what a member catching up is given, and what
// happens across a re-established group.
//
// These exist because a real group broke in a way none of the previous tests could have caught.
// Every group test there was used createDirectChat; the group path had no coverage at all. What
// went wrong was catch-up: control messages recorded their EPOCH but not their GROUP, and an epoch
// is unique only within a group. A re-established conversation starts counting again, so the
// retired group's epoch 1 and the live group's epoch 1 are different moments wearing the same
// number. One production conversation held 287 control messages across two group lifetimes and
// handed all of them to every member on every catch-up — which is every send.

// newGroup creates a group conversation owned by the caller and returns its id.
func newGroup(t *testing.T, f *fixture, adminTok, title string, memberIDs ...string) string {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations", adminTok, map[string]any{
		"kind": "group", "title": title, "memberIds": memberIDs,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rec.Code, rec.Body)
	}
	var group domain.Conversation
	if err := json.Unmarshal(rec.Body.Bytes(), &group); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	return group.ID
}

// controlMessages reads what a member catching up from `since` would be given.
func controlMessages(t *testing.T, f *fixture, token, conv string, since int64) []domain.ChatMessage {
	t.Helper()
	url := "/v1/conversations/" + conv + "/mls/commits?since=" + itoa(since)
	rec := f.do(http.MethodGet, url, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list commits: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Messages []domain.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode commits: %v", err)
	}
	return out.Messages
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// THE BUG. A conversation whose group was re-established must hand a catching-up member only the
// LIVE group's control messages. The retired group's share the same epoch numbers, and applying
// them to a group they do not belong to is at best wasted work and at worst a device that skips the
// real Commit because a stale one already moved it past that epoch.
func TestCatchUpExcludesARetiredGroupsControlMessages(t *testing.T) {
	f := newFixture(t)
	adminID, adminTok := f.user(t, "admin-life@pheme.test")
	memberID, memberTok := f.user(t, "member-life@pheme.test")
	conv := newGroup(t, f, adminTok, "Lifecycle", memberID)
	_ = adminID

	// The first group, driven up a few epochs.
	first := mlsState(t, f, adminTok, conv)
	if first.GroupID == "" {
		if code, st := commit(t, f, adminTok, conv, "group-one", 0); code != http.StatusOK {
			t.Fatalf("establish: %d", code)
		} else {
			first = st
		}
	}
	for epoch := first.Epoch; epoch < first.Epoch+3; epoch++ {
		if code, _ := commit(t, f, adminTok, conv, first.GroupID, epoch); code != http.StatusOK {
			t.Fatalf("commit at %d: %d", epoch, code)
		}
	}

	// Retire it and establish a new one, which starts its epochs again from zero.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/reset", adminTok, nil)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	second := mlsState(t, f, adminTok, conv)
	if code, st := commit(t, f, adminTok, conv, "group-two", second.Epoch); code != http.StatusOK {
		t.Fatalf("establish second: %d", code)
	} else {
		second = st
	}

	// What a member joining the new group is given.
	msgs := controlMessages(t, f, memberTok, conv, 0)
	if len(msgs) == 0 {
		t.Fatal("no control messages at all; the member cannot join")
	}
	for _, m := range msgs {
		if m.MLSGroupID != "" && m.MLSGroupID != second.GroupID {
			t.Errorf("catch-up included a control message for the RETIRED group %q (epoch %d); "+
				"its epochs collide with the live group's and applying it can skip the real Commit",
				m.MLSGroupID, m.MLSEpoch)
		}
	}
}

// Every control message must say which group it belongs to. Without it the filter above has nothing
// to work with, and a re-established conversation is indistinguishable from a long-lived one.
func TestControlMessagesRecordTheirGroup(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "stamp-admin@pheme.test")
	memberID, memberTok := f.user(t, "stamp-member@pheme.test")
	conv := newGroup(t, f, adminTok, "Stamped", memberID)

	state := mlsState(t, f, adminTok, conv)
	code, after := commit(t, f, adminTok, conv, "stamped-group", state.Epoch)
	if code != http.StatusOK {
		t.Fatalf("commit: %d", code)
	}

	msgs := controlMessages(t, f, memberTok, conv, 0)
	if len(msgs) == 0 {
		t.Fatal("no control messages")
	}
	for _, m := range msgs {
		if m.MLSGroupID != after.GroupID {
			t.Errorf("%s at epoch %d has groupId %q, want %q", m.ContentType, m.MLSEpoch, m.MLSGroupID, after.GroupID)
		}
	}
}

// Within one epoch the Welcome must precede the Commit, or a device being admitted meets a Commit
// for a group it has not joined.
func TestCatchUpOrdersWelcomeBeforeCommit(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "order-admin@pheme.test")
	memberID, memberTok := f.user(t, "order-member@pheme.test")
	conv := newGroup(t, f, adminTok, "Ordered", memberID)

	state := mlsState(t, f, adminTok, conv)
	if code, _ := commit(t, f, adminTok, conv, "ordered-group", state.Epoch); code != http.StatusOK {
		t.Fatalf("commit: %d", code)
	}

	msgs := controlMessages(t, f, memberTok, conv, 0)
	var sawCommitAt = map[int64]bool{}
	for _, m := range msgs {
		if m.ContentType == domain.ContentTypeMLSWelcome && sawCommitAt[m.MLSEpoch] {
			t.Errorf("a Welcome at epoch %d arrived AFTER the Commit for the same epoch", m.MLSEpoch)
		}
		if m.ContentType == domain.ContentTypeMLSCommit {
			sawCommitAt[m.MLSEpoch] = true
		}
	}
}

// A member catching up asks for everything past the epoch it already has, and must not be handed
// what it has already applied.
func TestCatchUpSinceExcludesWhatIsAlreadyApplied(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "since-admin@pheme.test")
	memberID, memberTok := f.user(t, "since-member@pheme.test")
	conv := newGroup(t, f, adminTok, "Since", memberID)

	state := mlsState(t, f, adminTok, conv)
	_, first := commit(t, f, adminTok, conv, "since-group", state.Epoch)
	_, second := commit(t, f, adminTok, conv, first.GroupID, first.Epoch)

	msgs := controlMessages(t, f, memberTok, conv, first.Epoch)
	for _, m := range msgs {
		if m.MLSEpoch <= first.Epoch {
			t.Errorf("catch-up from epoch %d returned a message at epoch %d", first.Epoch, m.MLSEpoch)
		}
	}
	if len(msgs) == 0 {
		t.Errorf("catch-up from %d returned nothing, but epoch %d exists", first.Epoch, second.Epoch)
	}
}

// Roster authority, as a table: who may do what to a group's membership.
func TestGroupRosterAuthority(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "roster-admin@pheme.test")
	memberID, memberTok := f.user(t, "roster-member@pheme.test")
	outsiderID, outsiderTok := f.user(t, "roster-outsider@pheme.test")
	thirdID, _ := f.user(t, "roster-third@pheme.test")
	conv := newGroup(t, f, adminTok, "Roster", memberID)

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		body   map[string]any
		want   int
	}{
		{"admin adds a member", http.MethodPost, "/members", adminTok, map[string]any{"userId": outsiderID}, http.StatusCreated},
		{"member cannot add", http.MethodPost, "/members", memberTok, map[string]any{"userId": thirdID}, http.StatusForbidden},
		{"outsider cannot add once removed", http.MethodPost, "/members", outsiderTok, map[string]any{"userId": thirdID}, http.StatusForbidden},
		{"member cannot remove another", http.MethodDelete, "/members/" + outsiderID, memberTok, nil, http.StatusForbidden},
		{"member may remove themselves", http.MethodDelete, "/members/" + memberID, memberTok, nil, http.StatusNoContent},
		{"admin may remove another", http.MethodDelete, "/members/" + outsiderID, adminTok, nil, http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(tc.method, "/v1/conversations/"+conv+tc.path, tc.token, tc.body)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d; body %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// A removed member must lose access immediately — to the roster, to the messages, and to the
// encrypted group's control history. Being able to keep reading the Commits would let them follow
// the group's shape after they were thrown out.
func TestRemovedMemberLosesAccessAtOnce(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "evict-admin@pheme.test")
	memberID, memberTok := f.user(t, "evict-member@pheme.test")
	conv := newGroup(t, f, adminTok, "Eviction", memberID)

	// While a member: everything works.
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/messages", memberTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("member reading messages: %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/commits?since=0", memberTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("member reading commits: %d", rec.Code)
	}

	rec := f.do(http.MethodDelete, "/v1/conversations/"+conv+"/members/"+memberID, adminTok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}

	for _, path := range []string{"/messages", "/mls/commits?since=0", "/mls", "/members"} {
		rec := f.do(http.MethodGet, "/v1/conversations/"+conv+path, memberTok, nil)
		if rec.Code == http.StatusOK {
			t.Errorf("a removed member can still GET %s", path)
		}
	}
	// And cannot post into it.
	rec = f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", memberTok,
		map[string]any{"ciphertext": []byte("nope"), "contentType": domain.ContentTypeMLSApplication})
	if rec.Code == http.StatusCreated {
		t.Error("a removed member can still send into the group")
	}
}

// An added member can read the group from the moment they are added — including the control history
// they need in order to join the encrypted group at all.
func TestAddedMemberCanReachWhatTheyNeedToJoin(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "join-admin@pheme.test")
	newcomerID, newcomerTok := f.user(t, "join-newcomer@pheme.test")
	conv := newGroup(t, f, adminTok, "Joining")

	// Before being added: nothing.
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/commits?since=0", newcomerTok, nil); rec.Code == http.StatusOK {
		t.Error("a non-member could read the group's control history")
	}

	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/members", adminTok, map[string]any{"userId": newcomerID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}

	for _, path := range []string{"/messages", "/mls", "/mls/commits?since=0", "/members"} {
		rec := f.do(http.MethodGet, "/v1/conversations/"+conv+path, newcomerTok, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("a newly added member cannot GET %s: %d", path, rec.Code)
		}
	}
}

// Only an admin may commit a removal of somebody else, and a non-admin's refusal must not be
// mistaken for the group being broken — the client falls back to adding without pruning.
func TestGroupRemovalCommitIsAdminOnly(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "cmt-admin@pheme.test")
	memberID, memberTok := f.user(t, "cmt-member@pheme.test")
	otherID, _ := f.user(t, "cmt-other@pheme.test")
	conv := newGroup(t, f, adminTok, "Commits", memberID, otherID)

	state := mlsState(t, f, adminTok, conv)
	_, established := commit(t, f, adminTok, conv, "cmt-group", state.Epoch)

	post := func(token string, removes []string, baseEpoch int64) int {
		rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", token, map[string]any{
			"groupId": established.GroupID, "baseEpoch": baseEpoch,
			"commit": []byte("c"), "welcome": []byte("w"), "removes": removes,
		})
		return rec.Code
	}

	if got := post(memberTok, []string{otherID}, established.Epoch); got != http.StatusForbidden {
		t.Errorf("non-admin removing another = %d, want 403", got)
	}
	// Removing only themselves is allowed — that is how leaving works.
	if got := post(memberTok, []string{memberID}, established.Epoch); got != http.StatusOK {
		t.Errorf("non-admin removing themselves = %d, want 200", got)
	}
	next := mlsState(t, f, adminTok, conv)
	if got := post(adminTok, []string{otherID}, next.Epoch); got != http.StatusOK {
		t.Errorf("admin removing another = %d, want 200", got)
	}
}

// Two members committing against the same epoch: exactly one wins, and the loser is told where the
// group actually is rather than being left to guess. Without this the group forks.
func TestConcurrentGroupCommitsSerialise(t *testing.T) {
	f := newFixture(t)
	_, adminTok := f.user(t, "race-admin@pheme.test")
	memberID, memberTok := f.user(t, "race-member@pheme.test")
	conv := newGroup(t, f, adminTok, "Race", memberID)

	state := mlsState(t, f, adminTok, conv)
	_, established := commit(t, f, adminTok, conv, "race-group", state.Epoch)

	first, firstState := commit(t, f, adminTok, conv, established.GroupID, established.Epoch)
	second, secondState := commit(t, f, memberTok, conv, established.GroupID, established.Epoch)

	if first != http.StatusOK {
		t.Fatalf("first commit = %d, want 200", first)
	}
	if second != http.StatusConflict {
		t.Fatalf("second commit at the same epoch = %d, want 409", second)
	}
	if secondState.Epoch != firstState.Epoch {
		t.Errorf("the conflict returned epoch %d, want the current %d — the loser must be told "+
			"where the group actually is", secondState.Epoch, firstState.Epoch)
	}
}

// A roster change has to be VISIBLE in the conversation. Without it the member list silently
// differs from what everyone remembers, and nobody can tell when or by whom.
func TestMembershipChangesAppearInTheConversation(t *testing.T) {
	f := newFixture(t)
	adminID, adminTok := f.user(t, "note-admin@pheme.test")
	joinerID, joinerTok := f.user(t, "note-joiner@pheme.test")
	leaverID, leaverTok := f.user(t, "note-leaver@pheme.test")
	conv := newGroup(t, f, adminTok, "Notes", leaverID)

	notes := func(token string) []struct {
		Action  string
		ActorID string
		UserID  string
	} {
		t.Helper()
		rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/messages", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("messages: %d %s", rec.Code, rec.Body)
		}
		var page struct {
			Messages []domain.ChatMessage `json:"messages"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var out []struct {
			Action  string
			ActorID string
			UserID  string
		}
		// The endpoint answers newest-first; read them in the order they happened.
		for i := len(page.Messages) - 1; i >= 0; i-- {
			m := page.Messages[i]
			if m.ContentType != domain.ContentTypeMembership {
				continue
			}
			var body struct {
				Action  string `json:"action"`
				ActorID string `json:"actorId"`
				UserID  string `json:"userId"`
			}
			if err := json.Unmarshal(m.Ciphertext, &body); err != nil {
				t.Fatalf("membership note is not readable json: %v (%q)", err, m.Ciphertext)
			}
			out = append(out, struct {
				Action  string
				ActorID string
				UserID  string
			}{body.Action, body.ActorID, body.UserID})
		}
		return out
	}

	// Added by an admin.
	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/members", adminTok,
		map[string]any{"userId": joinerID}); rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	// Removed by an admin.
	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv+"/members/"+joinerID, adminTok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove: %d", rec.Code)
	}
	// And someone leaving of their own accord.
	if rec := f.do(http.MethodDelete, "/v1/conversations/"+conv+"/members/"+leaverID, leaverTok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("leave: %d", rec.Code)
	}

	got := notes(adminTok)
	want := []struct{ action, actor, subject string }{
		{"added", adminID, joinerID},
		{"removed", adminID, joinerID},
		{"left", leaverID, leaverID},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d membership notes, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Action != w.action || got[i].ActorID != w.actor || got[i].UserID != w.subject {
			t.Errorf("note %d = %+v, want action=%s actor=%s user=%s", i, got[i], w.action, w.actor, w.subject)
		}
	}
	_ = joinerTok
}

// A membership note is not a message somebody wrote, so it must not buzz anyone's phone.
func TestMembershipNotesRaiseNoPushNotification(t *testing.T) {
	if isControlContent(domain.ContentTypeMembership) {
		return // already silent
	}
	t.Errorf("%s raises a push notification; a roster change is not something a person sent",
		domain.ContentTypeMembership)
}

// A history offer must be findable AFTER the moment it was posted.
//
// Offers are protocol traffic and are deliberately absent from the transcript, which left exactly
// one delivery route: the live stream, at the instant of posting. A device that asked for its
// history while the co-member answering was asleep — or that simply reconnected a second too late —
// never saw the answer, and since the request is made once per conversation per session it showed a
// blank history for the whole session. That is the ordinary case for a device that has just been
// restored from a recovery code and is still settling.
func TestAHistoryOfferCanBeCollectedAfterTheFact(t *testing.T) {
	f := newFixture(t)
	_, holderTok := f.user(t, "hist-holder@pheme.test")
	joinerID, joinerTok := f.user(t, "hist-joiner@pheme.test")
	conv := f.createGroup(t, holderTok, []string{joinerID})

	// The co-member answers a request by posting an offer.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", holderTok, map[string]any{
		"ciphertext":  []byte(`{"to":"someone","historyId":"blob-1"}`),
		"contentType": domain.ContentTypeMLSHistoryOffer,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("post offer: %d %s", rec.Code, rec.Body)
	}

	// It is NOT in the transcript — an offer is not something anyone wrote.
	rec = f.do(http.MethodGet, "/v1/conversations/"+conv+"/messages", joinerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages: %d", rec.Code)
	}
	var page struct {
		Messages []domain.ChatMessage `json:"messages"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	for _, m := range page.Messages {
		if m.ContentType == domain.ContentTypeMLSHistoryOffer {
			t.Error("a history offer appeared in the transcript; it is protocol traffic, not a message")
		}
	}

	// ...but it can still be collected, which is the whole point.
	rec = f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history-offers", joinerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history-offers: %d %s", rec.Code, rec.Body)
	}
	var offers struct {
		Messages []domain.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &offers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(offers.Messages) != 1 {
		t.Fatalf("got %d offers, want 1 — a device that missed the live delivery cannot find its history",
			len(offers.Messages))
	}
	if offers.Messages[0].ContentType != domain.ContentTypeMLSHistoryOffer {
		t.Errorf("got content type %q", offers.Messages[0].ContentType)
	}
}

// Only members. An offer is sealed to one device, but who is talking to whom is not public.
func TestHistoryOffersAreMembersOnly(t *testing.T) {
	f := newFixture(t)
	_, ownerTok := f.user(t, "ho-owner@pheme.test")
	memberID, _ := f.user(t, "ho-member@pheme.test")
	_, outsiderTok := f.user(t, "ho-outsider@pheme.test")
	conv := f.createGroup(t, ownerTok, []string{memberID})

	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/history-offers", outsiderTok, nil)
	if rec.Code == http.StatusOK {
		t.Errorf("a non-member could list a conversation's history offers: %d", rec.Code)
	}
}
