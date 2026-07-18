// Package push abstracts notification delivery to devices (FCM for mobile/web,
// Web Push for browsers). A logging no-op sender is provided for development.
package push

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Result reports the outcome of a single device delivery attempt.
type Result struct {
	DeviceID string
	Status   domain.DeliveryStatus
	Error    string
}

// ChatNotification is the push for a conversation message.
//
// It has no field for message CONTENT, and still does not. What it gained is a field for
// the message's CIPHERTEXT, and the distinction is the entire safety property: the server
// holds only ciphertext, cannot read it, and passes it through untouched for the recipient's
// own device to decrypt in a notification handler. A lock screen can now show the message,
// and the server still has no idea what it says.
//
// That is worth stating plainly because the type's original promise was "there is nowhere to
// put plaintext", and that promise is unchanged — Ciphertext is []byte straight off
// domain.ChatMessage, and nothing on the server ever decodes it. If a future change finds
// itself wanting to put a readable string here, it has gone wrong.
//
// The sender's name comes from conversation membership, not from the message.
type ChatNotification struct {
	ConversationID string
	MessageID      string
	SenderName     string
	// SenderAvatarID is the blob id of the sender's profile picture, or "" if they have
	// none. It is an id and not a URL because only a transport knows the public base to
	// resolve it against.
	SenderAvatarID string
	// Kind separates "you have a message" from "your phone is ringing". They are the same
	// push transport but not the same event: a call is worth waking a device for and stops
	// mattering in thirty seconds, whereas a message can wait and should not expire.
	Kind Kind
	// CallID is set on a call notification. It is what lets the service worker replace an
	// earlier ring for the same call rather than stacking a second one, and what lets a
	// cancelled call close its own notification instead of leaving a live-looking ring that
	// deep-links into a call nobody is on any more.
	CallID string
	// Privacy is the RECIPIENT's setting, and it is why this struct describes a push to one
	// group of people rather than to a conversation. Two members of the same conversation can
	// want different things on their lock screens, so the caller partitions recipients by this
	// value and builds one notification per group (see chat.notifyMembers).
	Privacy domain.NotificationPrivacy
	// Ciphertext is the encrypted message body, passed through for the recipient's device to
	// decrypt and display. Opaque here: the server cannot read it and never tries.
	//
	// It is attached only for a recipient whose setting asks for previews, and only for an
	// ordinary application message — see previewCiphertext, which is the single place that
	// decides, so the gate cannot be half-applied by one transport and not another.
	// GroupIDs are the MLS groups this conversation uses, newest first, sent alongside the
	// ciphertext so the device can decrypt it without having to already know which group the
	// message belongs to.
	//
	// A device only learns that mapping by OPENING a chat, so a freshly installed one knows it for
	// nothing and every preview fell back to "New message" until the user happened to visit each
	// conversation. Naming the group here removes the dependency: the recipient still needs the key
	// material, which only it has.
	//
	// Not a secret and not trusted. A group id is a routing label the server already holds; a wrong
	// one simply fails to decrypt, which is the same outcome as not sending it.
	GroupIDs []string

	Ciphertext []byte
	// ContentType is the message's MLS content type. It is here purely as a gate: only an
	// ordinary application message may be previewed, and protocol traffic must never be shipped
	// to a notification handler that would try to decrypt it.
	ContentType string
	// DeviceRendersPreview is whether the devices in THIS group run a build that can decrypt a
	// message and draw the notification itself.
	//
	// A second reason the fan-out partitions, and one that has nothing to do with privacy: a
	// preview reaches Android data-only, and a build that predates the handler ignores a
	// data-only message entirely — showing nothing at all rather than the generic text. So the
	// recipients are grouped by capability as well as by preference, and a user with two phones
	// on different app versions gets a preview on the updated one and the old notification on
	// the other, rather than silence on both.
	DeviceRendersPreview bool
}

// maxPreviewCiphertext caps how much ciphertext a push may carry.
//
// Web Push, APNs and FCM all cap a payload at 4 KB, and base64 inflates by a third. This
// leaves room for the title, body, ids and JSON scaffolding around it. A message too long to
// fit simply travels without its ciphertext and arrives as "New message" — a preview is a
// convenience, and dropping the push entirely because somebody wrote an essay would not be.
const maxPreviewCiphertext = 2400

// maxPushPayload is what a push service will accept, with room for the encryption Web Push
// applies on top: aes128gcm adds a record header, a 16-byte auth tag and padding, and the 4 KB
// cap is measured AFTER that. Measuring the plaintext against 4096 would put the real payload
// over the line.
const maxPushPayload = 4096 - 128

// payloadFits reports whether an assembled payload will survive delivery.
//
// It measures the Web Push JSON shape because that is the tightest of the three transports, and
// a payload sized for it fits FCM and APNs as well. Measuring rather than reasoning is the point:
// every field here is either user-supplied or built from user-supplied parts, and JSON escaping
// can multiply any of them by six.
func payloadFits(title, body, icon string, data map[string]string) bool {
	encoded, err := json.Marshal(webPushPayload{Title: title, Body: body, Icon: icon, Data: data})
	if err != nil {
		return false
	}
	return len(encoded) <= maxPushPayload
}

// previewCiphertext returns the ciphertext this notification may carry, or nil.
//
// Every condition that gates a preview lives here, in one place, because the failure mode of
// spreading them out is a transport that forgets one and ships message bodies to a device
// whose owner asked for a bare lock screen.
// maxPreviewGroups bounds how many groups travel with a preview. A conversation keeps its retired
// groups so their old messages still decrypt, and that list only ever grows — left unbounded it
// would eventually crowd out the ciphertext it exists to serve. Newest first, so the cut falls on
// the groups least likely to be carrying a message that has just been sent.
const maxPreviewGroups = 8

// previewGroupIDs renders the groups to try as a comma-separated list, or "" when there are none.
// Comma-separated because a push payload is flat strings; a group id is base64url and so cannot
// contain a comma.
func (n ChatNotification) previewGroupIDs() string {
	ids := n.GroupIDs
	if len(ids) > maxPreviewGroups {
		ids = ids[:maxPreviewGroups]
	}
	return strings.Join(ids, ",")
}

func (n ChatNotification) previewCiphertext() []byte {
	switch {
	case !n.Privacy.ShowsPreview():
		return nil // the recipient did not ask for previews
	case !n.DeviceRendersPreview:
		return nil // this build cannot draw one, and sending it anyway shows nothing at all
	case n.Kind != KindMessage:
		return nil // a call has no body to preview
	case n.ContentType != domain.ContentTypeMLSApplication:
		return nil // protocol traffic: never hand it to a decrypt-and-display path
	case len(n.Ciphertext) == 0 || len(n.Ciphertext) > maxPreviewCiphertext:
		return nil // nothing to send, or too big to fit
	default:
		return n.Ciphertext
	}
}

// identifies reports whether this notification may name the sender. A generic push still
// has to say something arrived, so the user knows to open the app; what it must not do is
// say who it was from, or show their face.
func (n ChatNotification) identifies() bool {
	return n.Privacy.ShowsSender()
}

// displayName is the name this notification may show, which is not the same thing as the
// name of the person who sent it: a recipient who asked not to be told gets the fallback.
//
// Every transport must go through this rather than reading SenderName directly. That rule
// exists because it was already broken once — the PushKit path built its own fallback, and
// so kept announcing the real caller on the CallKit screen of exactly the users who had
// asked it not to. A single accessor is what makes the setting hold everywhere by default
// instead of everywhere somebody remembered.
func (n ChatNotification) displayName() string {
	if !n.identifies() || n.SenderName == "" {
		return fallbackTitle
	}
	return n.SenderName
}

// Kind is what a chat push is telling the device about.
type Kind string

const (
	// KindMessage is the default: somebody sent a message.
	KindMessage Kind = ""
	// KindCall means somebody is calling right now.
	KindCall Kind = "call"
	// KindCallCancel means a call that was ringing is over — cancelled, answered elsewhere,
	// or missed. Without it a stale notification sits there looking live.
	KindCallCancel Kind = "call-cancel"
)

// ttl is how long the push service should keep trying to deliver this.
//
// A call is worthless the moment it stops ringing: delivering it two minutes later shows the
// user an incoming call that no longer exists. A message has no such deadline.
func (n ChatNotification) ttl() int {
	switch n.Kind {
	case KindCall, KindCallCancel:
		return 30
	default:
		return defaultTTL
	}
}

// Sender delivers notifications to a set of devices and reports per-device
// results. Channel messages are server-readable and carry their content; chat
// messages are encrypted and carry only who sent them.
type Sender interface {
	Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error)
	SendChat(ctx context.Context, n ChatNotification, devices []domain.Device) ([]Result, error)
}

// notification is the transport-neutral payload every Sender actually delivers.
// Both a channel message and a chat notification are reduced to one of these, so
// each transport has a single delivery path.
type notification struct {
	Title string
	Body  string
	// Image is a PHOTOGRAPH — a channel post's picture, which IS the notification and belongs in
	// the big hero slot below the text.
	Image string
	// Icon is a FACE — the sender's avatar, which belongs in the small round slot beside the title.
	//
	// Separate from Image because the platforms take them differently and putting one where the
	// other goes looks broken in both directions: an avatar in the hero slot fills the notification
	// with a blown-up 40px face, and a photo in the icon slot shrinks to an unreadable thumbnail.
	// The web service worker already distinguished these; FCM did not, which is how a chat push
	// came to render the sender's avatar full-width on Android.
	Icon string
	Data map[string]string
	// TTL is how long the push service should keep trying to deliver this, in seconds. A
	// ringing call is worthless once it has stopped ringing — delivering it two minutes late
	// shows somebody an incoming call that no longer exists.
	TTL int
	// Urgent means this has to wake a sleeping device, and it is what separates a call from
	// everything else. It buys high priority (so Doze does not sit on it) and, on Android, a
	// DATA-ONLY message — because a message carrying a notification payload is rendered by the
	// system tray and does not reliably start the background handler that raises the ringer.
	//
	// It is not a synonym for "important". A message is important; a message can also wait.
	Urgent bool
	// CollapseKey lets a later push REPLACE an earlier one rather than stack on top of it. A call
	// and its cancellation share one, so hanging up before an answer takes the ring back off the
	// other person's lock screen instead of leaving a dead call sitting there looking live.
	CollapseKey string
	// ThreadID groups notifications that belong to the same conversation, so ten messages from
	// one chat arrive as one expandable stack rather than ten separate banners burying
	// everything else. Distinct from CollapseKey: collapsing REPLACES a notification and loses
	// the earlier one, grouping keeps them all and merely files them together.
	ThreadID string
	// ClientRendered means the DEVICE draws this notification rather than the system tray,
	// because only the device can read the message it is going to show.
	//
	// It costs something, which is why it is not simply always on. On Android it forces a
	// DATA-ONLY high-priority message: a payload carrying a `notification` is drawn by the tray
	// while the app is backgrounded and does not reliably start the handler that would decrypt
	// it, and a normal-priority data message is held by Doze until the device next wakes. So a
	// preview means waking a dozing phone — which is the behaviour "a message can wait"
	// deliberately avoids for everybody else.
	//
	// Hence: set only for recipients who asked for previews. Everyone else keeps exactly the
	// delivery they had, and nobody pays in battery for a feature they did not turn on.
	//
	// On iOS it means mutable-content, which lets the NotificationServiceExtension rewrite the
	// alert before it is shown. The alert payload stays, so a device whose extension fails or
	// does not exist still shows the server's generic text.
	ClientRendered bool
}

func messageNotification(publicBaseURL string, msg domain.Message) notification {
	return notification{
		Title: msg.Title,
		Body:  msg.Body,
		Image: imageURL(publicBaseURL, msg),
		Data:  notificationData(msg),
		TTL:   defaultTTL,
	}
}

// The only text a chat push ever carries. All constants: the server cannot read a message
// and cannot hear a call, and must not imply that it can.
const (
	chatBody       = "New message"
	callBody       = "Incoming call"
	missedCallBody = "Missed call"
)

// fallbackTitle is what a notification is titled when it may not name anyone — either
// because the sender has no name to show, or because the recipient asked not to be told.
const fallbackTitle = "Pheme"

// defaultTTL is how long a push that is not time-critical may sit in a queue.
const defaultTTL = 60

func chatNotificationPayload(publicBaseURL string, n ChatNotification) notification {
	title := n.displayName()
	icon := ""
	if n.identifies() {
		icon = avatarURL(publicBaseURL, n.SenderAvatarID)
	}
	body := chatBody
	switch n.Kind {
	case KindCall:
		body = callBody
	case KindCallCancel:
		body = missedCallBody
	}
	// Enough to deep-link on tap; the client decrypts once it opens the chat. A call also
	// carries its id, so the service worker can replace an earlier ring for the same call
	// and close it again when the call ends.
	data := map[string]string{"conversationId": n.ConversationID, "messageId": n.MessageID}
	// Android groups client-side off conversationId, already above: FCM has no server-side
	// field for it (AndroidNotification exposes Tag, which REPLACES, but no group, which
	// bundles), so the client applies it. iOS is the opposite and groups from the server via
	// Aps.ThreadID — see buildMessage.
	//
	// The avatar travels in the data as well as in the notification payload, because the
	// Android client re-renders the notification itself when the app is foregrounded and has
	// no other way to reach it.
	if icon != "" {
		data["senderAvatar"] = icon
	}
	// The encrypted body, for the device to decrypt and show. Base64 because a push payload is
	// JSON; still ciphertext, and still unreadable to everything it passes through — this
	// server, Apple, Google, Mozilla. Absent unless every condition in previewCiphertext holds.
	//
	// The body stays the "New message" constant alongside it. A device that cannot decrypt —
	// an old client, a browser with no key material, a failed decrypt — then still shows
	// something sensible instead of a blank notification.
	clientRendered := false
	if ct := n.previewCiphertext(); ct != nil {
		data["ciphertext"] = base64.StdEncoding.EncodeToString(ct)
		clientRendered = true
		// Which groups to try. Without this a device that has never opened the chat has no way to
		// know, and falls back to the generic text — see ChatNotification.GroupIDs.
		if ids := n.previewGroupIDs(); ids != "" {
			data["groupIds"] = ids
		}
		// The title and body have to be IN THE DATA too, for the same reason a call's caller name
		// is: this goes out data-only so the client's handler runs at all, and a data-only message
		// has no notification payload to read them from. Without them a device that cannot decrypt
		// would have nothing to fall back to and would draw a blank notification — the one outcome
		// worse than a generic one.
		data["title"] = title
		data["body"] = body
		// Then MEASURE, and drop it again if the whole payload will not fit.
		//
		// A byte cap on the ciphertext alone is not enough, because it is not the only thing
		// competing for the 4 KB. A display name is bounded at 200 BYTES, but JSON escapes `<`
		// to `<` — so 200 of those become 1200 bytes in the payload, and a sender could
		// set such a name deliberately. The push service rejects an oversized payload WHOLESALE,
		// which would take down the entire notification rather than just its preview: a sender
		// could silence every recipient who had opted into previews, in every conversation they
		// were in, by editing their own profile.
		//
		// So nothing here is trusted to be the size it looks. The payload is built, weighed, and
		// the preview — the one part that is a convenience rather than the notification itself —
		// is what gives way.
		if !payloadFits(title, body, icon, data) {
			// Unwind the whole preview, not just its ciphertext: the data copies of the title and
			// body exist only to serve a client-rendered notification, and leaving them behind
			// would keep paying for a feature that is no longer happening.
			delete(data, "ciphertext")
			delete(data, "groupIds")
			delete(data, "title")
			delete(data, "body")
			// Back to a tray-rendered notification with it: there is nothing left for the device to
			// draw that the server has not already said.
			clientRendered = false
		}
	}

	isCall := n.Kind != KindMessage
	collapseKey := ""
	if isCall {
		data["kind"] = string(n.Kind)
		data["callId"] = n.CallID
		// The caller's name has to be IN THE DATA, not only in the title. A call is delivered as a
		// data-only message precisely so it starts the client's background handler, and a data-only
		// message has no title to read — so a name left only up there would reach nobody, and the
		// phone would ring for an anonymous stranger.
		//
		// The privacy setting applies here too: a recipient who does not want to be told who is
		// messaging them does not want their phone announcing who is calling them either, and the
		// call screen is the most conspicuous surface of the lot. They get "Pheme", and the real
		// name once they answer and the app is in front of them.
		data["callerName"] = title
		// One key per call, shared by the ring and its cancellation, so the cancel replaces the ring
		// rather than stacking a second notification under it.
		collapseKey = "call:" + n.CallID
	}

	return notification{
		Title: title,
		Body:  body,
		// A chat push carries a face, never a photo.
		Icon:        icon,
		Data:        data,
		TTL:         n.ttl(),
		Urgent:      isCall,
		CollapseKey: collapseKey,
		// Group by conversation, not by call: a ring has to stand on its own so it can be
		// replaced and closed independently of the chat's message stack.
		ThreadID:       threadID(n, isCall),
		ClientRendered: clientRendered,
	}
}

// threadID is the conversation a notification files itself under, or "" for a call.
func threadID(n ChatNotification, isCall bool) string {
	if isCall {
		return ""
	}
	return n.ConversationID
}

// avatarURL returns the absolute URL of a user's avatar blob, or "" when they have none
// or no public base URL is configured.
func avatarURL(publicBaseURL, avatarID string) string {
	if publicBaseURL == "" || avatarID == "" {
		return ""
	}
	return strings.TrimRight(publicBaseURL, "/") + "/v1/images/" + avatarID
}

// imageURL returns the absolute URL of a message's first image, or "" when the
// message has no images or no public base URL is configured.
func imageURL(publicBaseURL string, msg domain.Message) string {
	if publicBaseURL == "" || len(msg.Images) == 0 {
		return ""
	}
	return strings.TrimRight(publicBaseURL, "/") + "/v1/images/" + msg.Images[0].ID
}

// notificationData returns the data payload sent with a push notification: the
// message's user-supplied data plus channelId and messageId, so a notification
// tap can deep-link to the specific message.
func notificationData(msg domain.Message) map[string]string {
	data := make(map[string]string, len(msg.Data)+2)
	for k, v := range msg.Data {
		data[k] = v
	}
	data["channelId"] = msg.ChannelID
	data["messageId"] = msg.ID
	return data
}

// LogSender is a development Sender that logs instead of delivering. Every
// device is reported as skipped so the pipeline can be exercised without
// configured FCM/Web Push credentials.
type LogSender struct{}

// NewLogSender returns a no-op logging Sender.
func NewLogSender() *LogSender { return &LogSender{} }

// Send logs the message and marks each device as skipped.
func (s *LogSender) Send(_ context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	results := make([]Result, 0, len(devices))
	for _, d := range devices {
		slog.Info("push (dev no-op)",
			"channel", msg.ChannelID, "title", msg.Title, "device", d.ID, "platform", d.Platform)
		results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
	}
	return results, nil
}

// SendChat logs a chat notification. Only the conversation and sender are logged —
// there is no content to log, by design. The sender goes through displayName like every
// real transport, so a dev log shows what the device would actually have shown.
func (s *LogSender) SendChat(_ context.Context, n ChatNotification, devices []domain.Device) ([]Result, error) {
	results := make([]Result, 0, len(devices))
	for _, d := range devices {
		slog.Info("chat push (dev no-op)",
			"conversation", n.ConversationID, "sender", n.displayName(), "device", d.ID, "platform", d.Platform)
		results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
	}
	return results, nil
}

var _ Sender = (*LogSender)(nil)
