package chat

import (
	"context"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// WHO a notification reaches, as opposed to what it says.
//
// The existing push tests are almost all about payload SHAPING — what a notification contains under
// each privacy setting — plus TestChatPushSplitsByRecipientPrivacy, which proves two members who
// chose differently each get their own. What none of them covered is fan-out: whether every device
// of a member is reached, whether a member who has been REMOVED stops being reached, and whether a
// device that cannot render a preview is spared one.
//
// That distinction is not academic. A privacy setting that shapes the payload correctly and then
// sends it to the wrong device has leaked exactly as much as one that shapes it wrongly.

// pushDevice registers a push device with explicit properties, so a test can build the awkward
// combinations: a second device, one that cannot render previews, one belonging to the sender.
func (f *fixture) pushDevice(t *testing.T, userID, token string, canPreview bool) {
	t.Helper()
	if _, err := f.store.CreateDevice(context.Background(), domain.Device{
		UserID:           userID,
		Platform:         domain.PlatformAndroid,
		FCMToken:         token,
		CanRenderPreview: canPreview,
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}
}

// tokensNotified is every device token the fan-out addressed, across all sends.
func (f *fakePush) tokensNotified() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for _, group := range f.toDevs {
		for _, d := range group {
			out[d.FCMToken] = true
		}
	}
	return out
}

// A person with three devices signed in expects to be told on all three. Reaching only one is the
// same to them as not being told at all, because it is never the one in their hand.
func TestEveryDeviceOfEveryMemberIsNotified(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	senderID, senderTok := f.user(t, "fan-sender@pheme.test")
	memberID, _ := f.user(t, "fan-member@pheme.test")
	conv := f.createDirect(t, senderTok, memberID)

	f.pushDevice(t, memberID, "member-phone", true)
	f.pushDevice(t, memberID, "member-laptop", true)
	// The sender's own device must never be notified of their own message.
	f.pushDevice(t, senderID, "sender-phone", true)

	f.sendMessage(t, conv, senderTok)
	if !pusher.waitForPush(t) {
		t.Fatal("no push at all")
	}

	got := pusher.tokensNotified()
	for _, want := range []string{"member-phone", "member-laptop"} {
		if !got[want] {
			t.Errorf("device %q was not notified; a member with several devices must be told on all of them", want)
		}
	}
	if got["sender-phone"] {
		t.Error("the sender's own device was notified of their own message")
	}
}

// A member removed from a conversation must stop being notified about it. The roster is the only
// thing that decides this, and the fan-out reads it fresh on every message — so a removal takes
// effect on the very next one, with nothing to invalidate and no cache to go stale.
func TestARemovedMemberIsNoLongerNotified(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, adminTok := f.user(t, "rm-admin@pheme.test")
	stayID, _ := f.user(t, "rm-stay@pheme.test")
	goneID, _ := f.user(t, "rm-gone@pheme.test")
	conv := f.createGroup(t, adminTok, []string{stayID, goneID})

	f.pushDevice(t, stayID, "stays", true)
	f.pushDevice(t, goneID, "goes", true)

	// While they are a member, they are notified.
	f.sendMessage(t, conv, adminTok)
	if !pusher.waitForPush(t) {
		t.Fatal("no push before removal")
	}
	if !pusher.tokensNotified()["goes"] {
		t.Fatal("a member was not notified while they were still a member")
	}

	rec := f.do(http.MethodDelete, "/v1/conversations/"+conv+"/members/"+goneID, adminTok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}

	pusher.reset()
	f.sendMessage(t, conv, adminTok)
	if !pusher.waitForPush(t) {
		t.Fatal("no push after removal")
	}

	got := pusher.tokensNotified()
	if got["goes"] {
		t.Error("a removed member is still being notified about the conversation")
	}
	if !got["stays"] {
		t.Error("removing one member stopped the remaining member being notified")
	}
}

// Someone who leaves of their own accord stops being notified too — the same rule, reached by a
// different route, and the one a member can trigger without an admin.
func TestAMemberWhoLeavesIsNoLongerNotified(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, adminTok := f.user(t, "left-admin@pheme.test")
	stayID, _ := f.user(t, "left-stay@pheme.test")
	leaverID, leaverTok := f.user(t, "left-leaver@pheme.test")
	conv := f.createGroup(t, adminTok, []string{stayID, leaverID})

	f.pushDevice(t, stayID, "stays", true)
	f.pushDevice(t, leaverID, "left", true)

	rec := f.do(http.MethodDelete, "/v1/conversations/"+conv+"/members/"+leaverID, leaverTok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("leave: %d %s", rec.Code, rec.Body)
	}

	f.sendMessage(t, conv, adminTok)
	if !pusher.waitForPush(t) {
		t.Fatal("no push")
	}
	got := pusher.tokensNotified()
	if got["left"] {
		t.Error("someone who left the conversation is still being notified about it")
	}
	if !got["stays"] {
		t.Error("a member leaving stopped the remaining member being notified")
	}
}

// A device that has not declared it can render a preview must not be sent one, even when its owner
// asked for previews. A preview arrives DATA-ONLY, and a build without the handler ignores a
// data-only message completely — it shows NOTHING, not the generic text. So assuming the capability
// silently deletes notifications for everyone who has not updated, and the only signal would be
// users saying the app went quiet.
func TestCiphertextGoesOnlyToDevicesThatCanRenderIt(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, senderTok := f.user(t, "cap-sender@pheme.test")
	memberID, _ := f.user(t, "cap-member@pheme.test")
	conv := f.createDirect(t, senderTok, memberID)

	f.setPrivacy(t, memberID, domain.NotificationPrivacyPreview)
	f.pushDevice(t, memberID, "modern", true)
	f.pushDevice(t, memberID, "ancient", false)

	f.sendMessage(t, conv, senderTok)
	// One send per capability group, so wait for both.
	if !pusher.waitForPush(t) || !pusher.waitForPush(t) {
		t.Fatal("expected a push for each device capability")
	}

	rendersPreview := map[string]bool{}
	pusher.mu.Lock()
	for i, n := range pusher.sent {
		for _, d := range pusher.toDevs[i] {
			rendersPreview[d.FCMToken] = n.DeviceRendersPreview
		}
	}
	pusher.mu.Unlock()

	if !rendersPreview["modern"] {
		t.Error("a device that declared it can render a preview was not sent one")
	}
	if rendersPreview["ancient"] {
		t.Error("a device that never declared preview support was told to render a preview; " +
			"an older build ignores a data-only message completely and would show nothing at all")
	}
}

// A conversation nobody has a device for must not blow up, and must not stop the message being
// sent. A notification is a courtesy; the message is the point.
func TestAMessageSendsEvenWhenNobodyHasADevice(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, senderTok := f.user(t, "nodev-sender@pheme.test")
	memberID, _ := f.user(t, "nodev-member@pheme.test")
	conv := f.createDirect(t, senderTok, memberID)

	// No devices registered for anybody.
	f.sendMessage(t, conv, senderTok) // fails the test itself if the send does not return 201
	if len(pusher.notifications()) != 0 {
		t.Error("a notification was attempted for a conversation with no devices in it")
	}
}
