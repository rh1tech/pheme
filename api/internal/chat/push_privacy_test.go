package chat

import (
	"context"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// setPrivacy sets a user's notification privacy setting.
func (f *fixture) setPrivacy(t *testing.T, userID string, p domain.NotificationPrivacy) {
	t.Helper()
	if _, err := f.store.UpdateUserProfile(context.Background(), userID, domain.UserProfileUpdate{
		NotificationPrivacy: &p,
	}); err != nil {
		t.Fatalf("set privacy: %v", err)
	}
}

// sendMessage posts an ordinary encrypted message to a conversation.
func (f *fixture) sendMessage(t *testing.T, conv, token string) {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/conversations/"+conv+"/messages", token, map[string]any{
		"ciphertext":  []byte("opaque-mls-ciphertext"),
		"contentType": "application/mls",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: got %d (%s)", rec.Code, rec.Body.String())
	}
}

// The core of the design. What a push may reveal is the RECIPIENT's choice, so two members of one
// conversation who chose differently must each get what they asked for — which means the fan-out
// cannot build one payload and send it to everybody.
//
// Getting this wrong is not a cosmetic bug: whichever way a single shared payload resolved, it would
// either ignore a user's explicit privacy request or strip a name from somebody who never asked for
// that. The test is the reason the code partitions at all.
func TestChatPushSplitsByRecipientPrivacy(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	aliceID, aliceToken := f.user(t, "alice-priv@pheme.test")
	bobID, _ := f.user(t, "bob-priv@pheme.test")
	carolID, _ := f.user(t, "carol-priv@pheme.test")
	f.setDisplayName(t, aliceID, "Alice")

	bobDevice := f.device(t, bobID)
	carolDevice := f.device(t, carolID)

	// Bob wants a bare lock screen; Carol is happy with the default.
	f.setPrivacy(t, bobID, domain.NotificationPrivacyGeneric)

	conv := f.createGroup(t, aliceToken, []string{bobID, carolID})
	f.sendMessage(t, conv, aliceToken)

	// Two groups means two sends.
	if !pusher.waitForPush(t) || !pusher.waitForPush(t) {
		t.Fatal("expected two chat pushes, one per privacy group")
	}

	byDevice := map[string]domain.NotificationPrivacy{}
	names := map[string]string{}
	pusher.mu.Lock()
	for i, n := range pusher.sent {
		for _, d := range pusher.toDevs[i] {
			byDevice[d.ID] = n.Privacy
			names[d.ID] = n.SenderName
		}
	}
	pusher.mu.Unlock()

	if got := byDevice[bobDevice]; got != domain.NotificationPrivacyGeneric {
		t.Errorf("bob's device got privacy %q, want %q — he asked for a bare lock screen",
			got, domain.NotificationPrivacyGeneric)
	}
	if got := byDevice[carolDevice]; got != domain.NotificationPrivacyPreview {
		t.Errorf("carol's device got privacy %q, want %q — a new account starts with previews",
			got, domain.NotificationPrivacyPreview)
	}
	// Both notifications still carry the sender's name; it is the payload builder that withholds
	// it. That separation is deliberate — the fan-out decides WHO, the payload decides WHAT.
	if names[bobDevice] != "Alice" || names[carolDevice] != "Alice" {
		t.Errorf("sender name should reach both groups for the payload builder to apply the "+
			"setting, got bob=%q carol=%q", names[bobDevice], names[carolDevice])
	}
}

// When everybody agrees — overwhelmingly the common case — the partitioning must not multiply the
// work. One group, one send.
func TestChatPushSendsOnceWhenPrivacyIsUniform(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	aliceID, aliceToken := f.user(t, "alice-uni@pheme.test")
	bobID, _ := f.user(t, "bob-uni@pheme.test")
	carolID, _ := f.user(t, "carol-uni@pheme.test")
	f.setDisplayName(t, aliceID, "Alice")
	f.device(t, bobID)
	f.device(t, carolID)

	conv := f.createGroup(t, aliceToken, []string{bobID, carolID})
	f.sendMessage(t, conv, aliceToken)

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	if n := len(pusher.notifications()); n != 1 {
		t.Fatalf("expected exactly 1 send when every recipient agrees, got %d", n)
	}
	pusher.mu.Lock()
	devices := len(pusher.toDevs[0])
	pusher.mu.Unlock()
	if devices != 2 {
		t.Errorf("expected both recipients' devices in one group, got %d", devices)
	}
}

// The sender's avatar has to reach the payload builder, or the notification has nothing to show.
func TestChatPushCarriesSenderAvatar(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	aliceID, aliceToken := f.user(t, "alice-av@pheme.test")
	bobID, _ := f.user(t, "bob-av@pheme.test")
	f.setDisplayName(t, aliceID, "Alice")
	if _, err := f.store.SetUserAvatar(context.Background(), aliceID, "avatar-blob-1"); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	f.device(t, bobID)

	conv := f.createDirect(t, aliceToken, bobID)
	f.sendMessage(t, conv, aliceToken)

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	notes := pusher.notifications()
	if notes[0].SenderAvatarID != "avatar-blob-1" {
		t.Errorf("SenderAvatarID = %q, want avatar-blob-1", notes[0].SenderAvatarID)
	}
}

// THE UPGRADE-SAFETY TEST.
//
// Previews are on by default for new accounts, which is what people expect of a messenger. But
// an account that existed BEFORE the setting did must not be swept along: its owner never asked
// for message text on their lock screen, and turning it on during a deploy — silently, while
// they are not looking — is precisely the kind of change nobody forgives.
//
// A legacy account is one with no stored value, which is why new accounts get theirs written
// explicitly at creation (domain.User.WithNewUserDefaults). If that ever stops happening,
// "absent" starts meaning two different things and this test is what notices.
func TestChatPushDoesNotTurnOnPreviewsForLegacyAccounts(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	aliceID, aliceToken := f.user(t, "alice-legacy@pheme.test")
	bobID, _ := f.user(t, "bob-legacy@pheme.test")
	f.setDisplayName(t, aliceID, "Alice")
	f.device(t, bobID)

	// Bob predates the setting: clear the value the fixture wrote, leaving the field absent
	// exactly as it is for a row created before this feature shipped.
	f.clearPrivacy(t, bobID)

	conv := f.createDirect(t, aliceToken, bobID)
	f.sendMessage(t, conv, aliceToken)

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	n := pusher.notifications()[0]
	if n.Privacy.ShowsPreview() {
		t.Errorf("privacy %q resolved to previews for an account that predates the setting — "+
			"their lock screen would start showing message text without them ever asking",
			n.Privacy)
	}
	if !n.Privacy.ShowsSender() {
		t.Errorf("privacy %q stopped showing the sender for a legacy account; they should keep "+
			"exactly the behaviour they had", n.Privacy)
	}
}

// Ciphertext must reach the payload builder, or there is nothing for the device to decrypt.
func TestChatPushCarriesCiphertextAndContentType(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-ct@pheme.test")
	bobID, _ := f.user(t, "bob-ct@pheme.test")
	f.device(t, bobID)

	conv := f.createDirect(t, aliceToken, bobID)
	f.sendMessage(t, conv, aliceToken)

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	n := pusher.notifications()[0]
	if string(n.Ciphertext) != "opaque-mls-ciphertext" {
		t.Errorf("Ciphertext = %q, want the message's ciphertext", n.Ciphertext)
	}
	if n.ContentType != "application/mls" {
		t.Errorf("ContentType = %q, want application/mls — it is the gate that keeps protocol "+
			"traffic out of a decrypt-and-display path", n.ContentType)
	}
}

// THE UPGRADE-ORDERING TEST.
//
// A preview reaches Android as a DATA-ONLY message, because a notification payload would be drawn
// by the system tray before the app's handler could decrypt anything. A build that predates that
// handler ignores a data-only message completely — it does not show the generic text, it shows
// NOTHING.
//
// So the moment the server starts sending previews to a device that cannot draw them, that user's
// notifications simply stop. Silently. With new accounts defaulting to previews and app updates
// rolling out over days, this would land on real people during the very first deploy, and the only
// symptom would be "the app went quiet".
//
// Hence the capability flag, and hence this test: a device that has not said it can render a
// preview must never be sent one, whatever its owner's preference says.
func TestChatPushWithholdsPreviewFromDevicesThatCannotRenderIt(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-cap@pheme.test")
	bobID, _ := f.user(t, "bob-cap@pheme.test") // defaults to preview
	f.device(t, bobID)                          // registered WITHOUT the capability flag

	conv := f.createDirect(t, aliceToken, bobID)
	f.sendMessage(t, conv, aliceToken)

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	n := pusher.notifications()[0]
	if n.DeviceRendersPreview {
		t.Error("an old build was told it renders previews; its notifications would vanish entirely")
	}
}

// A user with two devices on different app versions gets a preview on the updated one and the
// ordinary notification on the other — rather than the wrong thing, or silence, on both.
func TestChatPushSplitsByDeviceCapability(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-mix@pheme.test")
	bobID, _ := f.user(t, "bob-mix@pheme.test") // defaults to preview
	oldPhone := f.device(t, bobID)
	newPhone := f.capableDevice(t, bobID)

	conv := f.createDirect(t, aliceToken, bobID)
	f.sendMessage(t, conv, aliceToken)

	// One send per capability group.
	if !pusher.waitForPush(t) || !pusher.waitForPush(t) {
		t.Fatal("expected two chat pushes, one per capability group")
	}

	renders := map[string]bool{}
	pusher.mu.Lock()
	for i, n := range pusher.sent {
		for _, d := range pusher.toDevs[i] {
			renders[d.ID] = n.DeviceRendersPreview
		}
	}
	pusher.mu.Unlock()

	if renders[newPhone] != true {
		t.Error("the updated device should get a preview")
	}
	if renders[oldPhone] != false {
		t.Error("the old device must not; it would show nothing at all")
	}
}

// One phone, one notification.
//
// Registering a device used to INSERT unconditionally for anything that was not a web push, and
// the mobile app registers on every launch — so a phone accumulated a row per start, the fan-out
// pushed to each of them, and the user got the same message twice, then three times. Web was safe
// only because it happened to key on its push endpoint.
func TestChatPushDoesNotDuplicateForARelaunchedApp(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-dup@pheme.test")
	bobID, _ := f.user(t, "bob-dup@pheme.test")

	// The same handset registering three times, as three app launches would.
	for i := 0; i < 3; i++ {
		if _, err := f.store.CreateDevice(context.Background(), domain.Device{
			UserID:           bobID,
			Platform:         "android",
			FCMToken:         "the-same-handset",
			CanRenderPreview: true,
		}); err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
	}

	conv := f.createDirect(t, aliceToken, bobID)
	f.sendMessage(t, conv, aliceToken)

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	pusher.mu.Lock()
	devices := 0
	for _, group := range pusher.toDevs {
		devices += len(group)
	}
	pusher.mu.Unlock()

	if devices != 1 {
		t.Errorf("pushed to %d device rows for one handset — the user gets the message %d times",
			devices, devices)
	}
}

// A device with no push address at all — a Mac, or a browser that declined notifications — must
// still get its own row. The device id is what the call answer-lock is keyed on, so collapsing
// them would make one machine unable to answer because another had claimed the call.
func TestDevicesWithoutPushAreNotCollapsed(t *testing.T) {
	f := newFixture(t)
	_, _ = f.user(t, "alice-nopush@pheme.test")
	bobID, _ := f.user(t, "bob-nopush@pheme.test")

	first, err := f.store.CreateDevice(context.Background(), domain.Device{UserID: bobID, Platform: "macos"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := f.store.CreateDevice(context.Background(), domain.Device{UserID: bobID, Platform: "macos"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID == second.ID {
		t.Error("two push-less devices were merged into one; each needs its own id to answer a call")
	}
}

// Ciphertext must not go to a push address that cannot be traced back to an MLS device.
//
// Revocation is the reason. Terminating a device removes its push rows by matching mlsDeviceId, so
// a row carrying none is not matched by that delete and outlives it. If such a row is still treated
// as preview-capable, it keeps being handed the ciphertext of messages the device has just been
// forbidden to read — which is the exact failure the two device registries were joined to prevent.
//
// domain.Device.MLSDeviceID documented this as enforced. It was not enforced anywhere: the only
// device-level condition was CanRenderPreview. A phone in production was in precisely this state,
// registered before its client had minted an MLS identity.
func TestChatPushWithholdsCiphertextFromUnrevocableDevices(t *testing.T) {
	f := newFixture(t)
	pusher := newFakePush()
	f.setPush(pusher)

	_, aliceToken := f.user(t, "alice-unrevocable@pheme.test")
	bobID, _ := f.user(t, "bob-unrevocable@pheme.test") // defaults to preview

	// Claims it can render previews, but nothing ties it to an MLS device — so revoking that device
	// would leave this row behind.
	if _, err := f.store.CreateDevice(context.Background(), domain.Device{
		UserID:           bobID,
		Platform:         "android",
		FCMToken:         "token-orphan",
		CanRenderPreview: true,
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}

	conv := f.createDirect(t, aliceToken, bobID)
	f.sendMessage(t, conv, aliceToken)

	if !pusher.waitForPush(t) {
		t.Fatal("expected a chat push")
	}
	n := pusher.notifications()[0]
	if n.DeviceRendersPreview {
		t.Error("a push address with no MLS device was treated as preview-capable; revoking the " +
			"device cannot remove this row, so it would go on receiving the ciphertext of messages " +
			"it is no longer allowed to read")
	}
	// The notification itself must still be sent — the device is unaccountable, not untrusted. It
	// gets the generic form until it registers again with its identity attached.
	if len(pusher.notifications()) == 0 {
		t.Error("no notification at all was sent; the device should still be told a message arrived")
	}
}

// Only a message a person wrote may raise a notification.
//
// The rule used to work the other way round: it listed the protocol content types it knew about and
// notified for everything else. Signing in on a new device posts a history request to every
// conversation the account is in, and a rejoin posts another — neither was on the list, so every
// member's phone lit up because somebody else had opened the app on a new phone. It happened on
// production and was reported as "each time medved logs in, everyone receives messages".
//
// application/mls-rejoin is in the table on purpose: it appears nowhere in this server's source. It
// is a content type the client invented and the server has never been told about, which is exactly
// the case the old default got wrong and the reason this rule is now an allowlist of one.
func TestOnlyHumanMessagesNotify(t *testing.T) {
	notifiable := map[string]bool{
		domain.ContentTypeMLSApplication: true,

		domain.ContentTypeMLSWelcome:        false,
		domain.ContentTypeMLSCommit:         false,
		domain.ContentTypeMLSDevice:         false,
		domain.ContentTypeMLSHistoryRequest: false,
		domain.ContentTypeMLSHistoryOffer:   false,
		domain.ContentTypeMembership:        false,
		domain.ContentTypeCallEvent:         false,
		"application/mls-rejoin":            false,
		"application/some-future-protocol":  false,
		"application/octet-stream":          false,
	}

	for contentType, wantNotify := range notifiable {
		if got := !isControlContent(contentType); got != wantNotify {
			if wantNotify {
				t.Errorf("%s does not notify, but it is what a person wrote — the message arrives "+
					"silently and nobody knows it is there", contentType)
			} else {
				t.Errorf("%s raises a notification. Nobody wrote it: every member of the "+
					"conversation is buzzed by protocol traffic, which is what happened when a "+
					"device signing in posted a history request to every chat the account is in",
					contentType)
			}
		}
	}
}
