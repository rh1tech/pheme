package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// GroupInfo: how a device with no leaf in the group joins one that already exists.
//
// A member added to a conversation cannot decrypt anything until it is inside the MLS group. The
// usual route is that somebody already inside commits it in. GroupInfo is the other route — an
// external join, where the newcomer builds its own Commit from the group's published state and
// needs nobody awake to let it in. Without it, a person added while every other member is offline
// waits, seeing an empty conversation, until someone opens the app.
//
// Both endpoints were uncovered.

// establishGroup creates the conversation's MLS group by posting the first Commit, which is how a
// group actually comes into existence. GroupInfo for a group that is not the conversation's current
// one is deliberately ignored by the store, so publishing without this would be testing nothing.
func establishGroup(t *testing.T, f *fixture, token, conv, groupID string) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/commit", token, map[string]any{
		"groupId": groupID, "baseEpoch": 0, "commit": []byte("first commit"),
	})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent {
		t.Fatalf("establish group %s = %d: %s", groupID, rec.Code, rec.Body)
	}
}

type groupInfoResponse struct {
	GroupID   string `json:"groupId"`
	Epoch     int64  `json:"epoch"`
	GroupInfo []byte `json:"groupInfo"`
}

func TestGroupInfoIsStoredAndReturned(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "gi-owner@pheme.test")
	other, _ := f.user(t, "gi-other@pheme.test")
	conv := f.group(t, ownerToken, "group info", other)
	establishGroup(t, f, ownerToken, conv, "group-1")

	blob := []byte("this stands in for a serialised GroupInfo")
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/group-info", ownerToken,
		map[string]any{"groupId": "group-1", "epoch": 7, "groupInfo": blob})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("post group info = %d: %s", rec.Code, rec.Body)
	}

	rec = f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/group-info", ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get group info = %d: %s", rec.Code, rec.Body)
	}
	var got groupInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.GroupID != "group-1" || got.Epoch != 7 {
		t.Errorf("got groupId=%q epoch=%d, want group-1/7", got.GroupID, got.Epoch)
	}
	// Byte-for-byte: this is cryptographic material, and a GroupInfo that survives the round trip
	// with so much as a byte changed is one an external join cannot use.
	if !bytes.Equal(got.GroupInfo, blob) {
		t.Errorf("groupInfo came back changed:\n got %q\nwant %q", got.GroupInfo, blob)
	}
}

// A newer publication replaces an older one. The group moves on, and an external join against a
// stale epoch produces a Commit the group will reject.
func TestGroupInfoIsReplacedByANewerEpoch(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "gi-owner2@pheme.test")
	other, _ := f.user(t, "gi-other2@pheme.test")
	conv := f.group(t, ownerToken, "moving group", other)
	establishGroup(t, f, ownerToken, conv, "group-2")

	for _, e := range []struct {
		epoch int
		blob  string
	}{{3, "old state"}, {4, "new state"}} {
		rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/group-info", ownerToken,
			map[string]any{"groupId": "group-2", "epoch": e.epoch, "groupInfo": []byte(e.blob)})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("publish epoch %d = %d: %s", e.epoch, rec.Code, rec.Body)
		}
	}

	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/group-info", ownerToken, nil)
	var got groupInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Epoch != 4 || string(got.GroupInfo) != "new state" {
		t.Errorf("got epoch=%d %q, want the newest publication; a joiner building on a stale epoch "+
			"produces a Commit the group rejects", got.Epoch, got.GroupInfo)
	}
}

// No GroupInfo published is a 404, and that is a real answer rather than a failure: it tells the
// client to announce itself and wait to be added the ordinary way.
func TestGroupInfoIsA404BeforeAnyIsPublished(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "gi-owner3@pheme.test")
	other, _ := f.user(t, "gi-other3@pheme.test")
	conv := f.group(t, ownerToken, "quiet group", other)

	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/group-info", ownerToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get with nothing published = %d, want 404", rec.Code)
	}
}

func TestGroupInfoRejectsIncompletePublications(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "gi-owner4@pheme.test")
	other, _ := f.user(t, "gi-other4@pheme.test")
	conv := f.group(t, ownerToken, "picky group", other)
	establishGroup(t, f, ownerToken, conv, "g")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"no group id", map[string]any{"epoch": 1, "groupInfo": []byte("x")}},
		{"no group info", map[string]any{"groupId": "g", "epoch": 1}},
		{"empty group info", map[string]any{"groupId": "g", "epoch": 1, "groupInfo": []byte{}}},
		{"negative epoch", map[string]any{"groupId": "g", "epoch": -1, "groupInfo": []byte("x")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/group-info", ownerToken, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400", tc.name, rec.Code)
			}
		})
	}

	// And none of that left anything behind to be served.
	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/group-info", ownerToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("a rejected publication still stored something (get = %d)", rec.Code)
	}
}

// GroupInfo is the material for joining a group. Only members may read or publish it — otherwise
// any signed-in stranger could external-join a conversation they were never added to.
func TestGroupInfoIsMembersOnly(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "gi-owner5@pheme.test")
	other, _ := f.user(t, "gi-other5@pheme.test")
	_, outsiderToken := f.user(t, "gi-outsider@pheme.test")
	conv := f.group(t, ownerToken, "private group", other)
	establishGroup(t, f, ownerToken, conv, "g5")

	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/group-info", ownerToken,
		map[string]any{"groupId": "g5", "epoch": 1, "groupInfo": []byte("state")}); rec.Code != http.StatusNoContent {
		t.Fatalf("publish = %d", rec.Code)
	}

	if rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/group-info", outsiderToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("an outsider read the group's join material (%d); they could external-join a "+
			"conversation nobody added them to", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/group-info", outsiderToken,
		map[string]any{"groupId": "g5", "epoch": 99, "groupInfo": []byte("forged")}); rec.Code != http.StatusNotFound {
		t.Errorf("an outsider published group state (%d)", rec.Code)
	}

	// The forgery did not land.
	rec := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/group-info", ownerToken, nil)
	var got groupInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got.GroupInfo) != "state" {
		t.Errorf("the stored group info is %q; an outsider overwrote it", got.GroupInfo)
	}
}

// Publishing for a group that is not the conversation's current one is ACCEPTED AND IGNORED.
//
// This looks like a silent failure and is worth being explicit about, because it is reachable: an
// MLS reset retires the current group, and a client that had GroupInfo in flight for the old one
// publishes into the void. The server answers 204 and stores nothing.
//
// It is nevertheless the intended contract rather than a bug. Both clients treat this endpoint as
// fire-and-forget — the mobile one says so in as many words, "best effort: a stale or missing one
// only costs a joiner a fall back to announcing itself" — and neither awaits the result. Answering
// 409 here, as the Commit endpoint does for a stale epoch, would raise errors in shipped builds on
// a path they deliberately do not handle, to report something they have already decided not to act
// on. Storing it would be worse: join material for a group that no longer exists, handed to the
// next device that asks.
//
// The test exists so the behaviour is a decision on the record rather than something nobody noticed.
func TestGroupInfoForARetiredGroupIsAcceptedAndIgnored(t *testing.T) {
	f := newFixture(t)
	_, ownerToken := f.user(t, "gi-stale@pheme.test")
	other, _ := f.user(t, "gi-stale2@pheme.test")
	conv := f.group(t, ownerToken, "reset group", other)
	establishGroup(t, f, ownerToken, conv, "group-old")

	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/group-info", ownerToken,
		map[string]any{"groupId": "group-old", "epoch": 1, "groupInfo": []byte("before the reset")}); rec.Code != http.StatusNoContent {
		t.Fatalf("publish = %d", rec.Code)
	}

	// Retire the group, as a recovery from an unusable one does.
	if rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/reset", ownerToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("reset = %d: %s", rec.Code, rec.Body)
	}

	// A straggler publishes for the group that has just been retired.
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/mls/group-info", ownerToken,
		map[string]any{"groupId": "group-old", "epoch": 2, "groupInfo": []byte("after the reset")})
	if rec.Code != http.StatusNoContent {
		t.Errorf("publishing for a retired group = %d, want 204 (accepted, ignored)", rec.Code)
	}

	// And nothing from the retired group is served to a joiner. Handing out join material for a
	// group that no longer exists would send the next device into one nobody else is in.
	get := f.do(http.MethodGet, "/v1/conversations/"+conv+"/mls/group-info", ownerToken, nil)
	if get.Code == http.StatusOK {
		var got groupInfoResponse
		if err := json.Unmarshal(get.Body.Bytes(), &got); err == nil && string(got.GroupInfo) == "after the reset" {
			t.Error("group info published for a retired group was stored and is being served to " +
				"joiners; they would external-join a group nobody is in")
		}
	}
}
