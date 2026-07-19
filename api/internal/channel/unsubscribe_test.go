package channel

import (
	"context"
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
