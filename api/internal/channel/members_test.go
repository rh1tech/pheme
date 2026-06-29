package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// createChannelMode creates a channel with an explicit subscription mode.
func (f *appFixture) createChannelMode(t *testing.T, token, name string, mode domain.SubscriptionMode) domain.Channel {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/channels", token,
		map[string]any{"name": name, "subscriptionMode": string(mode)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", rec.Code, rec.Body)
	}
	var ch domain.Channel
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	return ch
}

// registerDevice creates a web device for the given token and returns its id.
func (f *appFixture) registerDevice(t *testing.T, token string) string {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/devices", token, map[string]any{"platform": "web"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create device: %d %s", rec.Code, rec.Body)
	}
	var dev domain.Device
	_ = json.Unmarshal(rec.Body.Bytes(), &dev)
	return dev.ID
}

func (f *appFixture) activeDeviceCount(t *testing.T, channelID string) int {
	t.Helper()
	devs, err := f.store.ActiveDevicesForChannel(context.Background(), channelID)
	if err != nil {
		t.Fatalf("ActiveDevicesForChannel: %v", err)
	}
	return len(devs)
}

func TestSetPhetagAndUniqueness(t *testing.T) {
	f := newAppFixture(t)
	ownerA, _ := f.tokenFor(t, "a@b.com")
	ownerB, _ := f.tokenFor(t, "b@b.com")
	chA := f.createChannelMode(t, ownerA, "A", domain.ModeOpen)
	chB := f.createChannelMode(t, ownerB, "B", domain.ModeOpen)

	// Set a valid phetag.
	rec := f.do(http.MethodPatch, "/v1/channels/"+chA.ID, ownerA, map[string]any{"alias": "skg_news"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set alias: %d %s", rec.Code, rec.Body)
	}
	var got domain.Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Alias != "skg_news" {
		t.Fatalf("alias = %q, want skg_news", got.Alias)
	}

	// A different channel cannot take it, even with different case.
	rec = f.do(http.MethodPatch, "/v1/channels/"+chB.ID, ownerB, map[string]any{"alias": "SKG_News"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate alias status = %d, want 409; body=%s", rec.Code, rec.Body)
	}

	// Reserved prefix and malformed aliases are rejected.
	for _, bad := range []string{"ch_abcdef", "1bad", ".bad"} {
		rec = f.do(http.MethodPatch, "/v1/channels/"+chB.ID, ownerB, map[string]any{"alias": bad})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("alias %q status = %d, want 400; body=%s", bad, rec.Code, rec.Body)
		}
	}
}

func TestJoinedChannelsExcludesOwnedAfterSelfJoin(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	ch := f.createChannelMode(t, owner, "Medved", domain.ModeOpen)

	// Owner joins their own channel (e.g. to subscribe a device); this creates a
	// membership row but the channel must not appear under "joined channels".
	if rec := f.do(http.MethodPost, "/v1/channels/join", owner, map[string]any{"ref": ch.PublicID}); rec.Code != http.StatusCreated {
		t.Fatalf("self-join: %d %s", rec.Code, rec.Body)
	}

	rec := f.do(http.MethodGet, "/v1/channels/joined", owner, nil)
	var jl struct {
		Channels []joinedChannel `json:"channels"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &jl)
	if len(jl.Channels) != 0 {
		t.Fatalf("joined channels = %d (%+v), want 0 — owned channels must not duplicate", len(jl.Channels), jl.Channels)
	}
}

func TestJoinByTriggerIDAndPhetag(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, _ := f.tokenFor(t, "sub@b.com")
	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)
	if rec := f.do(http.MethodPatch, "/v1/channels/"+ch.ID, owner, map[string]any{"alias": "openchan"}); rec.Code != http.StatusOK {
		t.Fatalf("set alias: %d %s", rec.Code, rec.Body)
	}

	// Join by trigger ID (open channel → active immediately).
	rec := f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("join by id: %d %s", rec.Code, rec.Body)
	}
	var jr struct {
		Membership domain.ChannelMember `json:"membership"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &jr)
	if jr.Membership.Status != domain.MemberActive {
		t.Fatalf("status = %q, want active", jr.Membership.Status)
	}

	// Joined channels list now contains it.
	rec = f.do(http.MethodGet, "/v1/channels/joined", subTok, nil)
	var jl struct {
		Channels []joinedChannel `json:"channels"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &jl)
	if len(jl.Channels) != 1 || jl.Channels[0].ID != ch.ID {
		t.Fatalf("joined channels = %+v, want one (%s)", jl.Channels, ch.ID)
	}

	// A second user can join by phetag (case-insensitive).
	sub2, _ := f.tokenFor(t, "sub2@b.com")
	rec = f.do(http.MethodPost, "/v1/channels/join", sub2, map[string]any{"ref": "OpenChan"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("join by phetag: %d %s", rec.Code, rec.Body)
	}

	// Unknown ref → 404.
	rec = f.do(http.MethodPost, "/v1/channels/join", sub2, map[string]any{"ref": "nope"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("join unknown ref status = %d, want 404", rec.Code)
	}
}

func TestApprovalQueueApproveDenyAndBan(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, subUser := f.tokenFor(t, "sub@b.com")
	ch := f.createChannelMode(t, owner, "Approval", domain.ModeApproval)
	dev := f.registerDevice(t, subTok)

	// Join an approval channel → pending; not yet delivering.
	rec := f.do(http.MethodPost, "/v1/channels/join", subTok,
		map[string]any{"ref": ch.PublicID, "deviceId": dev})
	if rec.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	if n := f.activeDeviceCount(t, ch.ID); n != 0 {
		t.Fatalf("active devices before approval = %d, want 0", n)
	}

	// Owner sees the pending request.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/approvals", owner, nil)
	var appr struct {
		Members []struct {
			UserID string `json:"userId"`
			Email  string `json:"email"`
			Status string `json:"status"`
		} `json:"members"`
		Total int `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &appr)
	if len(appr.Members) != 1 || appr.Members[0].UserID != subUser.ID || appr.Members[0].Email != "sub@b.com" {
		t.Fatalf("approvals = %+v, want one for %s", appr.Members, subUser.ID)
	}

	// Approve → membership active and the device now delivers.
	rec = f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/approvals/"+subUser.ID, owner, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if n := f.activeDeviceCount(t, ch.ID); n != 1 {
		t.Fatalf("active devices after approval = %d, want 1", n)
	}

	// Ban → delivery stops; unban → restored.
	if rec = f.do(http.MethodPatch, "/v1/channels/"+ch.ID+"/members/"+subUser.ID, owner,
		map[string]any{"status": "blocked"}); rec.Code != http.StatusOK {
		t.Fatalf("ban: %d %s", rec.Code, rec.Body)
	}
	if n := f.activeDeviceCount(t, ch.ID); n != 0 {
		t.Fatalf("active devices after ban = %d, want 0", n)
	}
	if rec = f.do(http.MethodPatch, "/v1/channels/"+ch.ID+"/members/"+subUser.ID, owner,
		map[string]any{"status": "active"}); rec.Code != http.StatusOK {
		t.Fatalf("unban: %d %s", rec.Code, rec.Body)
	}
	if n := f.activeDeviceCount(t, ch.ID); n != 1 {
		t.Fatalf("active devices after unban = %d, want 1", n)
	}
}

func TestDenyRemovesPendingMember(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, subUser := f.tokenFor(t, "sub@b.com")
	ch := f.createChannelMode(t, owner, "Approval", domain.ModeApproval)

	if rec := f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID}); rec.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/approvals/"+subUser.ID, owner, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("deny: %d %s", rec.Code, rec.Body)
	}
	// No longer pending.
	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/approvals", owner, nil)
	var appr struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &appr)
	if appr.Total != 0 {
		t.Fatalf("pending total = %d, want 0", appr.Total)
	}
}

func TestChannelAdminRoleGrantsModeration(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, subUser := f.tokenFor(t, "sub@b.com")
	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)

	// Join, then a plain member cannot see approvals.
	if rec := f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID}); rec.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/approvals", subTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member approvals status = %d, want 403", rec.Code)
	}

	// Promote to channel admin → can now moderate.
	if rec := f.do(http.MethodPatch, "/v1/channels/"+ch.ID+"/members/"+subUser.ID, owner,
		map[string]any{"role": "admin"}); rec.Code != http.StatusOK {
		t.Fatalf("promote: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/approvals", subTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin approvals status = %d, want 200", rec.Code)
	}
	// But a channel admin cannot change the phetag (owner-only).
	if rec := f.do(http.MethodPatch, "/v1/channels/"+ch.ID, subTok, map[string]any{"alias": "nope"}); rec.Code != http.StatusForbidden {
		t.Fatalf("admin alias status = %d, want 403", rec.Code)
	}
}

func TestLeaveAndRemoveMember(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, subUser := f.tokenFor(t, "sub@b.com")
	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)

	// Owner cannot leave their own channel.
	if rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/membership", owner, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("owner leave status = %d, want 400", rec.Code)
	}

	// Member joins then leaves.
	if rec := f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID}); rec.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/membership", subTok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("leave: %d %s", rec.Code, rec.Body)
	}
	rec := f.do(http.MethodGet, "/v1/channels/joined", subTok, nil)
	var jl struct {
		Channels []joinedChannel `json:"channels"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &jl)
	if len(jl.Channels) != 0 {
		t.Fatalf("joined after leave = %d, want 0", len(jl.Channels))
	}

	// Re-join then owner removes them.
	_ = f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID})
	if rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/members/"+subUser.ID, owner, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	// Owner cannot be removed via the members endpoint.
	if rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/members/"+ch.OwnerID, owner, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("remove owner status = %d, want 400", rec.Code)
	}
}

func TestGetChannelRelationForMemberAndStranger(t *testing.T) {
	f := newAppFixture(t)
	owner, _ := f.tokenFor(t, "owner@b.com")
	subTok, _ := f.tokenFor(t, "sub@b.com")
	stranger, _ := f.tokenFor(t, "x@b.com")
	ch := f.createChannelMode(t, owner, "Open", domain.ModeOpen)
	_ = f.do(http.MethodPost, "/v1/channels/join", subTok, map[string]any{"ref": ch.PublicID})

	// Owner sees isOwner true.
	rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID, owner, nil)
	var or struct {
		IsOwner bool `json:"isOwner"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &or)
	if rec.Code != http.StatusOK || !or.IsOwner {
		t.Fatalf("owner get: code=%d isOwner=%v", rec.Code, or.IsOwner)
	}

	// Member sees the channel with status active.
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID, subTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("member get status = %d, want 200", rec.Code)
	}

	// Stranger gets 404 (existence not leaked).
	rec = f.do(http.MethodGet, "/v1/channels/"+ch.ID, stranger, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger get status = %d, want 404", rec.Code)
	}
}
