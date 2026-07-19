package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Unsubscribing a device from a channel.
//
// The endpoint takes a device id from the query string and acts on it. It was authenticated but not
// AUTHORISED: it unsubscribed whatever id it was handed, without checking the device belonged to
// the caller. Anyone signed in who learned somebody else's device id could quietly stop their phone
// receiving a channel, and the victim would see no error anywhere — only notifications that stopped
// arriving, which is indistinguishable from the push service having a bad day.
//
// No route currently hands out another user's push device id, so this was latent rather than
// exploitable. It is worth closing anyway: "you would have to learn an id we do not publish" is a
// property of today's routes rather than of this handler, and the next route to return a device is
// under no obligation to remember that.

// deviceFor registers a push device for a user and subscribes it to a channel.
func deviceFor(t *testing.T, f *appFixture, userID, channelID string) domain.Device {
	t.Helper()
	d, err := f.store.CreateDevice(context.Background(), domain.Device{
		UserID: userID, Platform: domain.PlatformWeb, WebPushSub: `{"endpoint":"https://example.test"}`,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if _, err := f.store.Subscribe(context.Background(), domain.Subscription{
		ChannelID: channelID, DeviceID: d.ID, Status: domain.SubActive,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return d
}

// channelFor creates a channel owned by the given user.
func channelFor(t *testing.T, f *appFixture, ownerID, name string) domain.Channel {
	t.Helper()
	ch, err := f.store.CreateChannel(context.Background(), domain.Channel{
		PublicID: "pub-" + name, OwnerID: ownerID, Name: name,
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return ch
}

// subscribed reports whether the device still has an active subscription.
func subscribed(t *testing.T, f *appFixture, channelID, deviceID string) bool {
	t.Helper()
	sub, err := f.store.SubscriptionForDevice(context.Background(), channelID, deviceID)
	if err != nil {
		return false
	}
	return sub.Status == domain.SubActive
}

func TestUnsubscribingYourOwnDeviceWorks(t *testing.T) {
	f := newAppFixture(t)
	token, user := f.tokenFor(t, "unsub-owner@pheme.test")
	ch := channelFor(t, f, user.ID, "news")
	device := deviceFor(t, f, user.ID, ch.ID)

	if !subscribed(t, f, ch.ID, device.ID) {
		t.Fatal("setup: the device is not subscribed")
	}
	rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/subscribe?deviceId="+device.ID, token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe = %d: %s", rec.Code, rec.Body)
	}
	if subscribed(t, f, ch.ID, device.ID) {
		t.Error("the endpoint answered 204 but the device is still subscribed")
	}
}

// THE ONE THAT MATTERS. Somebody else's device must not be unsubscribed.
func TestUnsubscribingSomebodyElsesDeviceDoesNothing(t *testing.T) {
	f := newAppFixture(t)
	victimToken, victim := f.tokenFor(t, "victim@pheme.test")
	attackerToken, _ := f.tokenFor(t, "attacker@pheme.test")
	_ = victimToken

	ch := channelFor(t, f, victim.ID, "victims-channel")
	device := deviceFor(t, f, victim.ID, ch.ID)

	rec := f.do(http.MethodDelete,
		"/v1/channels/"+ch.ID+"/subscribe?deviceId="+device.ID, attackerToken, nil)

	if subscribed(t, f, ch.ID, device.ID) == false {
		t.Fatal("another user unsubscribed this device; their phone stops receiving the channel " +
			"and nothing anywhere reports an error")
	}
	// Answered as though it had succeeded. A distinct status would confirm which device ids exist
	// and who owns them — the enumeration the ownership check exists to prevent.
	if rec.Code != http.StatusNoContent {
		t.Errorf("unsubscribing a device that is not yours = %d; the answer must not reveal "+
			"whether the id exists", rec.Code)
	}
}

func TestUnsubscribeNeedsADeviceID(t *testing.T) {
	f := newAppFixture(t)
	token, user := f.tokenFor(t, "unsub-nodevice@pheme.test")
	ch := channelFor(t, f, user.ID, "needs-device")

	rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/subscribe", token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unsubscribe with no deviceId = %d, want 400", rec.Code)
	}
}

// Unsubscribing something already gone is not an error. A client retrying after a dropped
// connection must not be told its own state is wrong.
func TestUnsubscribingTwiceIsNotAnError(t *testing.T) {
	f := newAppFixture(t)
	token, user := f.tokenFor(t, "unsub-twice@pheme.test")
	ch := channelFor(t, f, user.ID, "twice")
	device := deviceFor(t, f, user.ID, ch.ID)

	for i := 0; i < 2; i++ {
		rec := f.do(http.MethodDelete,
			"/v1/channels/"+ch.ID+"/subscribe?deviceId="+device.ID, token, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unsubscribe attempt %d = %d: %s", i+1, rec.Code, rec.Body)
		}
	}
}

func TestUnsubscribeRequiresSigningIn(t *testing.T) {
	f := newAppFixture(t)
	_, user := f.tokenFor(t, "unsub-anon@pheme.test")
	ch := channelFor(t, f, user.ID, "anon")
	device := deviceFor(t, f, user.ID, ch.ID)

	rec := f.do(http.MethodDelete, "/v1/channels/"+ch.ID+"/subscribe?deviceId="+device.ID, "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated unsubscribe = %d, want 401", rec.Code)
	}
	if !subscribed(t, f, ch.ID, device.ID) {
		t.Error("an unauthenticated request unsubscribed the device")
	}
}

// What a client asks before it decides what to render: am I the owner here, a member, or a
// stranger? Every "manage this channel" affordance in both clients hangs off the answer, so a
// wrong one either hides controls from someone who has them or offers controls that will be
// refused — the second being the one that looks like a broken product.
func TestMembershipReportsTheCallersRelationToTheChannel(t *testing.T) {
	f := newAppFixture(t)
	ownerToken, owner := f.tokenFor(t, "rel-owner@pheme.test")
	ch := channelFor(t, f, owner.ID, "relations")

	t.Run("the owner", func(t *testing.T) {
		got := membershipOf(t, f, ch.ID, ownerToken)
		if got["isOwner"] != true {
			t.Errorf("the channel's owner is not reported as owner: %v", got)
		}
	})

	t.Run("an active member", func(t *testing.T) {
		member := seedUser(t, f.store, "rel-member@pheme.test", domain.RoleUser)
		joinChannel(t, f, ch.ID, member.ID, domain.RoleUser)
		token, _, _, err := f.tokens.Issue(member.ID, string(domain.RoleUser))
		if err != nil {
			t.Fatalf("issue token: %v", err)
		}

		got := membershipOf(t, f, ch.ID, token)
		if got["isOwner"] == true {
			t.Errorf("an ordinary member is reported as the owner: %v", got)
		}
		if got["status"] != string(domain.MemberActive) {
			t.Errorf("status = %v, want active", got["status"])
		}
	})

	t.Run("a channel admin", func(t *testing.T) {
		admin := seedUser(t, f.store, "rel-admin@pheme.test", domain.RoleUser)
		joinChannel(t, f, ch.ID, admin.ID, domain.RoleAdmin)
		token, _, _, err := f.tokens.Issue(admin.ID, string(domain.RoleUser))
		if err != nil {
			t.Fatalf("issue token: %v", err)
		}

		got := membershipOf(t, f, ch.ID, token)
		if got["role"] != string(domain.RoleAdmin) {
			t.Errorf("role = %v, want admin; the client hides the controls this person has",
				got["role"])
		}
		if got["isOwner"] == true {
			t.Errorf("a channel admin is reported as the owner: %v", got)
		}
	})

	t.Run("a stranger", func(t *testing.T) {
		token, _ := f.tokenFor(t, "rel-stranger@pheme.test")

		got := membershipOf(t, f, ch.ID, token)
		if got["isOwner"] == true {
			t.Errorf("someone who has never joined is reported as the owner: %v", got)
		}
		if got["role"] == string(domain.RoleAdmin) {
			t.Errorf("someone who has never joined is reported as an admin: %v", got)
		}
		// A non-member gets an answer rather than a 404 — the endpoint exists to tell them they
		// are not a member, which is how the client knows to offer "join".
		if got["status"] == string(domain.MemberActive) {
			t.Errorf("someone who has never joined is reported as an active member: %v", got)
		}
	})
}

func TestMembershipOnAChannelThatIsNotThere(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "rel-missing@pheme.test")

	rec := f.do(http.MethodGet, "/v1/channels/000000000000000000000000/membership", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("membership on a missing channel = %d, want 404", rec.Code)
	}
}

func TestMembershipRequiresSigningIn(t *testing.T) {
	f := newAppFixture(t)
	_, owner := f.tokenFor(t, "rel-anon@pheme.test")
	ch := channelFor(t, f, owner.ID, "anon-relations")

	if rec := f.do(http.MethodGet, "/v1/channels/"+ch.ID+"/membership", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated membership = %d, want 401", rec.Code)
	}
}

// membershipOf reads the endpoint's answer for one caller.
func membershipOf(t *testing.T, f *appFixture, channelID, token string) map[string]any {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/channels/"+channelID+"/membership", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("membership = %d: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	return got
}
