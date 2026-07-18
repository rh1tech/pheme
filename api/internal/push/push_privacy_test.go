package push

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

const testBaseURL = "https://pheme.example/"

// The default — an empty setting, which is every user who has never opened the screen — must keep
// showing what it showed before the setting existed. A privacy control that silently changes the
// behaviour of people who never asked for it is a regression, however well-meant.
func TestChatPayload_DefaultShowsSenderAndAvatar(t *testing.T) {
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		SenderName:     "Ada",
		SenderAvatarID: "img-1",
	})

	if n.Title != "Ada" {
		t.Errorf("title = %q, want Ada", n.Title)
	}
	// ICON, not Image. An avatar is a face and belongs in the small round slot; Image is the
	// hero-picture slot, and an avatar put there renders full-width — which is exactly what
	// Android did with it.
	if want := "https://pheme.example/v1/images/img-1"; n.Icon != want {
		t.Errorf("icon = %q, want %q", n.Icon, want)
	}
	if n.Image != "" {
		t.Errorf("image = %q, want empty: a chat push carries a face, never a photograph, and "+
			"FCM renders Image full-width", n.Image)
	}
	if n.Data["senderAvatar"] != n.Icon {
		t.Errorf("Data[senderAvatar] = %q, want it to match Icon %q — the Android client draws the "+
			"avatar itself and has no other way to reach it", n.Data["senderAvatar"], n.Icon)
	}
}

// The whole point of the setting. Generic must give up BOTH the name and the face: an avatar is a
// photograph of the person messaging you, so leaking it while withholding their name would defeat
// the setting while appearing to honour it.
func TestChatPayload_GenericRevealsNoIdentity(t *testing.T) {
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		SenderName:     "Ada",
		SenderAvatarID: "img-1",
		Privacy:        domain.NotificationPrivacyGeneric,
	})

	if n.Title != fallbackTitle {
		t.Errorf("title = %q, want %q — a generic push must not name the sender", n.Title, fallbackTitle)
	}
	if n.Icon != "" {
		t.Errorf("icon = %q, want empty: an avatar is a picture of the sender, so showing it "+
			"identifies them just as surely as their name does", n.Icon)
	}
	if n.Data["senderAvatar"] != "" {
		t.Errorf("Data[senderAvatar] = %q, want empty", n.Data["senderAvatar"])
	}
	// Still has to say something arrived, or the user has no reason to open the app.
	if n.Body != chatBody {
		t.Errorf("body = %q, want %q", n.Body, chatBody)
	}
	// And still has to deep-link, since the tap target is not secret — the user is unlocking to it.
	if n.Data["conversationId"] != "conv-1" {
		t.Errorf("Data[conversationId] = %q, want conv-1", n.Data["conversationId"])
	}
}

// A ringing phone is the loudest notification there is. Somebody who does not want their lock screen
// naming the people who message them certainly does not want it announcing, at volume, who is
// calling — and callerName travels in the DATA, which is a separate code path from the title and so
// a separate chance to leak.
func TestChatPayload_GenericHidesCallerName(t *testing.T) {
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID: "conv-1",
		SenderName:     "Ada",
		Kind:           KindCall,
		CallID:         "call-1",
		Privacy:        domain.NotificationPrivacyGeneric,
	})

	if got := n.Data["callerName"]; got != fallbackTitle {
		t.Errorf("Data[callerName] = %q, want %q — the call screen is the most conspicuous surface "+
			"of the lot, and this is the field it reads", got, fallbackTitle)
	}
	if n.Title != fallbackTitle {
		t.Errorf("title = %q, want %q", n.Title, fallbackTitle)
	}
}

// Grouping is per conversation, so a busy chat arrives as one expandable stack rather than burying
// every other notification on the lock screen.
func TestChatPayload_MessagesGroupByConversation(t *testing.T) {
	first := chatNotificationPayload("", ChatNotification{ConversationID: "conv-1", MessageID: "m1"})
	second := chatNotificationPayload("", ChatNotification{ConversationID: "conv-1", MessageID: "m2"})
	other := chatNotificationPayload("", ChatNotification{ConversationID: "conv-2", MessageID: "m3"})

	if first.ThreadID != "conv-1" {
		t.Errorf("ThreadID = %q, want conv-1", first.ThreadID)
	}
	if first.ThreadID != second.ThreadID {
		t.Error("two messages in one conversation must share a thread id, or they stack separately")
	}
	if first.ThreadID == other.ThreadID {
		t.Error("different conversations must not share a thread id, or they collapse into one stack")
	}

	// It reaches iOS, which is the only platform that can act on it from the server.
	msg := buildMessage("token", first)
	if msg.APNS.Payload.Aps.ThreadID != "conv-1" {
		t.Errorf("aps thread-id = %q, want conv-1", msg.APNS.Payload.Aps.ThreadID)
	}
}

// A call must NOT be grouped with the conversation's messages: it has to be replaceable and
// dismissable on its own, and filing it into the message stack takes that away.
func TestChatPayload_CallIsNotGroupedWithMessages(t *testing.T) {
	call := chatNotificationPayload("", ChatNotification{
		ConversationID: "conv-1", Kind: KindCall, CallID: "call-1",
	})
	if call.ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty: a ring must stand alone so its cancellation can "+
			"replace it and close it", call.ThreadID)
	}
}

// An avatar url cannot be built without a public base, and half a url is worse than none — a broken
// image on a lock screen looks like a broken app.
func TestChatPayload_NoAvatarWithoutPublicBaseURL(t *testing.T) {
	n := chatNotificationPayload("", ChatNotification{SenderName: "Ada", SenderAvatarID: "img-1"})
	if n.Icon != "" {
		t.Errorf("icon = %q, want empty when no public base URL is configured", n.Icon)
	}
}

// A sender with no display name and no username still has to produce a usable notification.
func TestChatPayload_FallsBackWhenSenderHasNoName(t *testing.T) {
	n := chatNotificationPayload("", ChatNotification{ConversationID: "conv-1"})
	if n.Title != fallbackTitle {
		t.Errorf("title = %q, want %q", n.Title, fallbackTitle)
	}
}

// The setting is a closed set. An unrecognised value read back from an older or newer server must
// not be treated as valid, because the zero value it would fall back to is the most revealing one.
func TestNotificationPrivacy_Valid(t *testing.T) {
	tests := []struct {
		privacy domain.NotificationPrivacy
		want    bool
	}{
		{domain.NotificationPrivacyPreview, true},
		{domain.NotificationPrivacySender, true},
		{domain.NotificationPrivacyGeneric, true},
		// Not valid INPUT. It is a legacy storage state meaning "predates the setting", and a
		// client that means sender has to say sender — see Effective.
		{domain.NotificationPrivacy(""), false},
		{domain.NotificationPrivacy("nonsense"), false},
	}
	for _, tt := range tests {
		if got := tt.privacy.Valid(); got != tt.want {
			t.Errorf("NotificationPrivacy(%q).Valid() = %v, want %v", tt.privacy, got, tt.want)
		}
	}
}

// The leak this feature shipped with on its first pass, pinned so it cannot come back.
//
// The PushKit path built its own name fallback instead of going through displayName, so it kept
// announcing the real caller on the CallKit screen — full-screen, ahead of the lock screen — of
// exactly the users who had asked it not to. Every OTHER transport was correct, which is what made
// it easy to miss: the setting appeared to work everywhere anyone thought to look.
func TestVoIPPayload_GenericHidesCallerName(t *testing.T) {
	p := voipPayloadFor(ChatNotification{
		ConversationID: "conv-1",
		CallID:         "call-1",
		SenderName:     "Ada",
		Kind:           KindCall,
		Privacy:        domain.NotificationPrivacyGeneric,
	})

	if p.CallerName != fallbackTitle {
		t.Errorf("CallerName = %q, want %q — the CallKit screen takes over the whole device, "+
			"and a recipient who asked not to be told who is messaging them has if anything "+
			"asked harder not to be told this", p.CallerName, fallbackTitle)
	}
	// The call still has to arrive: iOS kills an app that fails to report a VoIP push to CallKit.
	if p.CallID != "call-1" || p.ConversationID != "conv-1" {
		t.Errorf("a generic call must still be a deliverable call, got %+v", p)
	}
}

// And the default still names the caller, or every iPhone user loses caller ID.
func TestVoIPPayload_DefaultNamesCaller(t *testing.T) {
	p := voipPayloadFor(ChatNotification{CallID: "call-1", SenderName: "Ada", Kind: KindCall})
	if p.CallerName != "Ada" {
		t.Errorf("CallerName = %q, want Ada", p.CallerName)
	}
}

// displayName is the one accessor every transport must use. If a new one reads SenderName directly
// it will pass its own tests and leak in production — as the VoIP path did.
func TestDisplayName_FallsBackForGenericAndForNoName(t *testing.T) {
	tests := []struct {
		name string
		n    ChatNotification
		want string
	}{
		{"named sender, default privacy", ChatNotification{SenderName: "Ada"}, "Ada"},
		{"named sender, generic", ChatNotification{
			SenderName: "Ada", Privacy: domain.NotificationPrivacyGeneric,
		}, fallbackTitle},
		{"no name at all", ChatNotification{}, fallbackTitle},
	}
	for _, tt := range tests {
		if got := tt.n.displayName(); got != tt.want {
			t.Errorf("%s: displayName() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// decodePreview returns the ciphertext a payload carries, and whether it carried one.
func decodePreview(t *testing.T, n notification) ([]byte, bool) {
	t.Helper()
	raw, ok := n.Data["ciphertext"]
	if !ok {
		return nil, false
	}
	out, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("payload ciphertext is not valid base64: %v", err)
	}
	return out, true
}

// A recipient who asked for previews gets the ciphertext to decrypt on-device.
func TestChatPayload_PreviewCarriesCiphertext(t *testing.T) {
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           "Ada",
		Privacy:              domain.NotificationPrivacyPreview,
		Ciphertext:           []byte("opaque-bytes"),
		ContentType:          domain.ContentTypeMLSApplication,
		DeviceRendersPreview: true,
	})

	got, ok := decodePreview(t, n)
	if !ok {
		t.Fatal("no ciphertext in the payload: the device has nothing to decrypt and the preview " +
			"silently degrades to the generic body")
	}
	if string(got) != "opaque-bytes" {
		t.Errorf("ciphertext = %q, want opaque-bytes", got)
	}
	// The generic body rides along regardless, so a client that cannot decrypt — an old build, a
	// browser with no key material, a failed decrypt — still shows something sensible.
	if n.Body != chatBody {
		t.Errorf("body = %q, want the %q fallback to survive alongside the ciphertext", n.Body, chatBody)
	}
}

// THE GATE. Each of these is a way a message body could reach a device whose owner did not ask
// for one, or a decrypt path that must never see it. Table-driven because the failure is the
// same in every case and the point is that NONE of them slips through.
func TestChatPayload_CiphertextIsWithheld(t *testing.T) {
	base := ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           "Ada",
		Privacy:              domain.NotificationPrivacyPreview,
		Ciphertext:           []byte("opaque-bytes"),
		ContentType:          domain.ContentTypeMLSApplication,
		DeviceRendersPreview: true,
	}
	with := func(f func(*ChatNotification)) ChatNotification {
		n := base
		n.Ciphertext = append([]byte(nil), base.Ciphertext...)
		f(&n)
		return n
	}

	tests := []struct {
		name string
		n    ChatNotification
		why  string
	}{
		{
			"recipient chose sender-only",
			with(func(n *ChatNotification) { n.Privacy = domain.NotificationPrivacySender }),
			"they asked to see who, not what",
		},
		{
			"recipient chose generic",
			with(func(n *ChatNotification) { n.Privacy = domain.NotificationPrivacyGeneric }),
			"they asked for a bare lock screen",
		},
		{
			"legacy account with no stored setting",
			with(func(n *ChatNotification) { n.Privacy = "" }),
			"an account predating the setting must not gain previews in a deploy",
		},
		{
			"control traffic, not a human message",
			with(func(n *ChatNotification) { n.ContentType = "application/mls-commit" }),
			"a commit handed to a decrypt-and-display path could move the epoch",
		},
		{
			"a build that cannot draw one",
			with(func(n *ChatNotification) { n.DeviceRendersPreview = false }),
			"an old app ignores a data-only push entirely and shows NOTHING, not even generic text",
		},
		{
			"a call",
			with(func(n *ChatNotification) { n.Kind = KindCall; n.CallID = "call-1" }),
			"a call has no body to preview",
		},
		{
			"ciphertext too large for a push payload",
			with(func(n *ChatNotification) { n.Ciphertext = make([]byte, maxPreviewCiphertext+1) }),
			"an oversized payload is rejected wholesale, losing the notification entirely",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := chatNotificationPayload(testBaseURL, tt.n)
			if _, ok := decodePreview(t, n); ok {
				t.Errorf("payload carried the message body: %s", tt.why)
			}
		})
	}
}

// Right at the limit it must still go, or the cap silently costs more than it should.
func TestChatPayload_CiphertextAtTheLimitIsSent(t *testing.T) {
	n := chatNotificationPayload("", ChatNotification{
		ConversationID:       "conv-1",
		Privacy:              domain.NotificationPrivacyPreview,
		Ciphertext:           make([]byte, maxPreviewCiphertext),
		ContentType:          domain.ContentTypeMLSApplication,
		DeviceRendersPreview: true,
	})
	if _, ok := decodePreview(t, n); !ok {
		t.Error("a ciphertext exactly at the cap should still be sent")
	}
}

// The whole payload has to fit inside what Web Push, APNs and FCM will accept. If the cap and
// the scaffolding around it ever add up to more than 4 KB, previews start being dropped by the
// push service — as a delivery failure, with nothing useful in any log.
func TestChatPayload_MaximumPayloadFitsPushLimits(t *testing.T) {
	n := chatNotificationPayload("https://pheme.example", ChatNotification{
		ConversationID:       "conversation-id-of-a-realistic-length-0123456789",
		MessageID:            "message-id-of-a-realistic-length-0123456789",
		SenderName:           "A Sender With A Fairly Long Display Name Indeed",
		SenderAvatarID:       "avatar-blob-id-of-a-realistic-length-0123456789",
		Privacy:              domain.NotificationPrivacyPreview,
		Ciphertext:           make([]byte, maxPreviewCiphertext),
		ContentType:          domain.ContentTypeMLSApplication,
		DeviceRendersPreview: true,
	})

	encoded, err := json.Marshal(webPushPayload{
		Title: n.Title, Body: n.Body, Icon: n.Icon, Data: n.Data,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Budget against the ENCRYPTED size, not the plaintext one. Web Push applies its 4 KB cap
	// after aes128gcm, which adds a record header, a 16-byte auth tag and padding — roughly 100
	// bytes. Measuring the plaintext against 4096 would leave the real payload over the line.
	const pushLimit = 4096 - 128
	if len(encoded) > pushLimit {
		t.Errorf("worst-case payload is %d bytes, over the %d-byte push limit: lower "+
			"maxPreviewCiphertext (currently %d)", len(encoded), pushLimit, maxPreviewCiphertext)
	}
	t.Logf("worst-case payload: %d bytes of %d", len(encoded), pushLimit)
}

// domain.ContentTypeMLSApplication is duplicated here because importing the chat package would be a
// cycle. A duplicated constant is a constant that drifts, so this pins the copy to the
// original: if chat renames its content type, previews would silently stop working — every
// message would fail the gate and arrive as "New message", with nothing to explain why.
func TestContentTypeApplicationMatchesChat(t *testing.T) {
	const chatPackageValue = "application/mls" // chat.contentTypeMLSApplication
	if domain.ContentTypeMLSApplication != chatPackageValue {
		t.Errorf("push says %q, chat says %q — previews will silently never fire",
			domain.ContentTypeMLSApplication, chatPackageValue)
	}
}

// A SENDER MUST NOT BE ABLE TO SILENCE A RECIPIENT'S NOTIFICATIONS.
//
// DisplayName is bounded at 200 BYTES and accepts any characters. JSON escapes `<` to a
// six-byte `<`, so 200 of them become ~1200 bytes in the payload — and on top of a
// ~3200-byte base64 ciphertext that clears the 4 KB a push service will accept.
//
// The failure that makes it an attack rather than a glitch: an oversized payload is rejected
// WHOLESALE. Not "the preview is dropped" — the entire notification never arrives. So a sender
// could mute every recipient who had opted into previews, in every conversation they shared, by
// editing their own profile. Nothing in the payload is trusted to be the size it looks; it is
// measured, and the preview gives way.
func TestChatPayload_HostileDisplayNameCannotSuppressTheNotification(t *testing.T) {
	// Exactly what profile validation permits: maxFieldLen is a byte bound with no charset rule.
	const maxFieldLen = 200
	hostile := strings.Repeat("<", maxFieldLen)

	n := chatNotificationPayload("https://pheme.example", ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           hostile,
		SenderAvatarID:       "avatar-1",
		Privacy:              domain.NotificationPrivacyPreview,
		Ciphertext:           make([]byte, maxPreviewCiphertext),
		ContentType:          domain.ContentTypeMLSApplication,
		DeviceRendersPreview: true,
	})

	encoded, err := json.Marshal(webPushPayload{
		Title: n.Title, Body: n.Body, Icon: n.Icon, Data: n.Data,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) > maxPushPayload {
		t.Errorf("payload is %d bytes, over the %d-byte limit: the push service rejects it "+
			"outright and the recipient gets NO notification at all — a sender can mute people "+
			"by renaming themselves", len(encoded), maxPushPayload)
	}
	// The preview is what gave way, not the notification.
	if _, ok := decodePreview(t, n); ok {
		t.Error("ciphertext survived in an oversized payload; it should have been dropped")
	}
	if n.Body != chatBody {
		t.Errorf("body = %q, want the %q fallback — the notification must still arrive and still "+
			"be useful", n.Body, chatBody)
	}
	if n.Data["conversationId"] != "conv-1" {
		t.Error("the deep link must survive: the notification still has to go somewhere on tap")
	}
	t.Logf("hostile-name payload: %d bytes of %d (preview dropped)", len(encoded), maxPushPayload)
}

// The same name with no preview attached must still deliver — the degradation has to bottom out
// at a working notification, not at a smaller broken one.
func TestChatPayload_HostileDisplayNameStillDeliversWithoutPreview(t *testing.T) {
	hostile := strings.Repeat("<", 200)
	n := chatNotificationPayload("https://pheme.example", ChatNotification{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		SenderName:     hostile,
		Privacy:        domain.NotificationPrivacySender,
	})
	encoded, _ := json.Marshal(webPushPayload{
		Title: n.Title, Body: n.Body, Icon: n.Icon, Data: n.Data,
	})
	if len(encoded) > maxPushPayload {
		t.Errorf("payload is %d bytes, over the %d-byte limit even with no preview", len(encoded), maxPushPayload)
	}
}

// A preview has to be drawn by the DEVICE, because only the device can read it — and on Android
// that means the push must arrive data-only and high-priority.
//
// Both halves matter and both are easy to get wrong silently:
//   - a payload carrying a `notification` is drawn by the system tray before the app's handler
//     runs, so the preview would never happen and the user would just see "New message";
//   - a normal-priority data message is held by Doze until the phone next wakes, so the
//     notification would arrive correct but an hour late.
func TestBuildMessage_PreviewIsDataOnlyAndHighPriorityOnAndroid(t *testing.T) {
	n := chatNotificationPayload("", ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           "Ada",
		Privacy:              domain.NotificationPrivacyPreview,
		Ciphertext:           []byte("opaque"),
		ContentType:          domain.ContentTypeMLSApplication,
		DeviceRendersPreview: true,
	})
	msg := buildMessage("token", n)

	if msg.Notification != nil {
		t.Error("a preview must be data-only: with a notification payload the Android tray draws " +
			"it before the handler that would decrypt it ever runs")
	}
	if msg.Android.Priority != "high" {
		t.Errorf("Android priority = %q, want high — a normal-priority data message is held by "+
			"Doze until the device next wakes", msg.Android.Priority)
	}
	// The generic title and body must travel in the data, because a data-only message has no
	// notification payload to read them from. Without them a device that cannot decrypt would
	// draw a blank notification, which is worse than a generic one.
	if msg.Data["title"] != "Ada" {
		t.Errorf("Data[title] = %q, want Ada", msg.Data["title"])
	}
	if msg.Data["body"] != chatBody {
		t.Errorf("Data[body] = %q, want %q", msg.Data["body"], chatBody)
	}
	// iOS still gets a real alert — its extension rewrites this rather than drawing from scratch,
	// so a phone with no extension, or one whose extension fails, still shows something.
	if msg.APNS.Payload.Aps.Alert == nil {
		t.Error("the APNs alert must survive: it is the fallback when the extension cannot run")
	}
	if !msg.APNS.Payload.Aps.MutableContent {
		t.Error("mutable-content must be set, or the iOS extension is never invoked and the " +
			"generic text is all anyone ever sees")
	}
}

// And the cost is paid ONLY by people who asked for previews. Everyone else keeps exactly the
// delivery they had — tray-rendered, normal priority, no waking a dozing phone for a chat message.
func TestBuildMessage_NonPreviewDeliveryIsUnchanged(t *testing.T) {
	for _, privacy := range []domain.NotificationPrivacy{
		domain.NotificationPrivacySender,
		domain.NotificationPrivacyGeneric,
		"", // legacy account
	} {
		n := chatNotificationPayload("", ChatNotification{
			ConversationID: "conv-1",
			SenderName:     "Ada",
			Privacy:        privacy,
			Ciphertext:     []byte("opaque"),
			ContentType:    domain.ContentTypeMLSApplication,
		})
		msg := buildMessage("token", n)

		if msg.Notification == nil {
			t.Errorf("privacy %q: lost its notification payload — the tray should still draw this",
				privacy)
		}
		if msg.Android.Priority != "normal" {
			t.Errorf("privacy %q: Android priority = %q, want normal. A message can wait, and "+
				"nobody should pay in battery for a feature they did not turn on",
				privacy, msg.Android.Priority)
		}
		if msg.APNS.Payload.Aps.MutableContent {
			t.Errorf("privacy %q: mutable-content set on a push with nothing to rewrite", privacy)
		}
	}
}

// When the preview is dropped for size, the delivery shaping must be dropped WITH it — otherwise
// the push goes out data-only with no ciphertext, and the client renders a notification from data
// fields that are no longer there.
func TestBuildMessage_OversizedPreviewFallsBackToTrayRendering(t *testing.T) {
	n := chatNotificationPayload("https://pheme.example", ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           strings.Repeat("<", 200), // JSON-escapes to ~1200 bytes
		Privacy:              domain.NotificationPrivacyPreview,
		Ciphertext:           make([]byte, maxPreviewCiphertext),
		ContentType:          domain.ContentTypeMLSApplication,
		DeviceRendersPreview: true,
	})
	msg := buildMessage("token", n)

	if msg.Notification == nil {
		t.Fatal("the preview was dropped for size, so the tray must draw this one instead — " +
			"otherwise nothing draws it at all")
	}
	if msg.Data["title"] != "" || msg.Data["body"] != "" {
		t.Error("data copies of title/body outlived the preview they existed for")
	}
	if msg.Android.Priority != "normal" {
		t.Error("a push with no preview must not still be waking a dozing phone")
	}
}

// A device learns which MLS group a conversation uses only by OPENING the chat. A freshly
// installed one therefore knows it for nothing, and every preview it received fell back to the
// generic text until the user happened to visit each conversation in turn — a preview feature that
// silently does not work on exactly the devices most likely to be testing it.
func TestChatPayload_PreviewCarriesGroupIDs(t *testing.T) {
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           "Ada",
		Privacy:              domain.NotificationPrivacyPreview,
		DeviceRendersPreview: true,
		Kind:                 KindMessage,
		ContentType:          domain.ContentTypeMLSApplication,
		Ciphertext:           []byte("ciphertext"),
		GroupIDs:             []string{"group-now", "group-retired"},
	})

	if got := n.Data["groupIds"]; got != "group-now,group-retired" {
		t.Errorf("Data[groupIds] = %q, want the current group first then the retired ones", got)
	}
}

// The ids are only useful to a device that is going to decrypt. Sending them to one that is not
// tells it which group a conversation uses for no reason at all.
func TestChatPayload_NoGroupIDsWithoutAPreview(t *testing.T) {
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		SenderName:     "Ada",
		Privacy:        domain.NotificationPrivacySender,
		Kind:           KindMessage,
		ContentType:    domain.ContentTypeMLSApplication,
		Ciphertext:     []byte("ciphertext"),
		GroupIDs:       []string{"group-now"},
	})

	if got, ok := n.Data["groupIds"]; ok {
		t.Errorf("Data[groupIds] = %q on a notification with no preview, want it absent", got)
	}
}

// When the payload will not fit, the preview is unwound — and the group ids are part of the
// preview, not of the notification. Leaving them behind would keep paying bytes for a feature that
// is no longer happening, in the one situation where there are no bytes to spare.
func TestChatPayload_OversizeDropsGroupIDsWithTheRest(t *testing.T) {
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           strings.Repeat("<", 200),
		Privacy:              domain.NotificationPrivacyPreview,
		DeviceRendersPreview: true,
		Kind:                 KindMessage,
		ContentType:          domain.ContentTypeMLSApplication,
		Ciphertext:           []byte(strings.Repeat("x", maxPreviewCiphertext-1)),
		GroupIDs:             []string{"group-now"},
	})

	if _, ok := n.Data["ciphertext"]; ok {
		t.Fatal("ciphertext survived an oversize payload; this test no longer tests the unwind")
	}
	if got, ok := n.Data["groupIds"]; ok {
		t.Errorf("Data[groupIds] = %q after the preview was unwound, want it dropped too", got)
	}
}

// The retired-group list only ever grows. Unbounded, it would eventually crowd out the ciphertext
// it exists to serve.
func TestChatPayload_GroupIDsAreBounded(t *testing.T) {
	many := make([]string, 0, maxPreviewGroups+5)
	for i := 0; i < maxPreviewGroups+5; i++ {
		many = append(many, "group")
	}
	n := chatNotificationPayload(testBaseURL, ChatNotification{
		ConversationID:       "conv-1",
		MessageID:            "msg-1",
		SenderName:           "Ada",
		Privacy:              domain.NotificationPrivacyPreview,
		DeviceRendersPreview: true,
		Kind:                 KindMessage,
		ContentType:          domain.ContentTypeMLSApplication,
		Ciphertext:           []byte("ciphertext"),
		GroupIDs:             many,
	})

	if got := len(strings.Split(n.Data["groupIds"], ",")); got != maxPreviewGroups {
		t.Errorf("sent %d groups, want it capped at %d", got, maxPreviewGroups)
	}
}
