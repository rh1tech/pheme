// Package domain defines the core Pheme entities shared across services.
package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// SubscriptionMode controls who may subscribe a device to a channel.
type SubscriptionMode string

const (
	// ModeOpen lets any user with the public channel ID subscribe immediately.
	ModeOpen SubscriptionMode = "open"
	// ModeApproval requires the channel owner to approve each subscriber.
	ModeApproval SubscriptionMode = "approval"
)

// Platform identifies the kind of device receiving notifications.
type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformWeb     Platform = "web"
	// PlatformMacOS is the desktop app. It registers a device like any other — the call answer-lock is
	// keyed on a server-issued device id, so a Mac must have one or it cannot pick up a call — but it
	// carries no push token of any kind. There is no PushKit on macOS, so it cannot be rung while it is
	// closed; it hears about a call over the live stream, like the web does, which is the honest
	// arrangement for a machine that is either open or off.
	PlatformMacOS Platform = "macos"
)

// SubscriptionStatus is the lifecycle state of a channel subscription.
type SubscriptionStatus string

const (
	SubActive  SubscriptionStatus = "active"
	SubPending SubscriptionStatus = "pending"
	SubBlocked SubscriptionStatus = "blocked"
)

// DeliveryStatus records the outcome of a single push attempt.
type DeliveryStatus string

const (
	DeliverySent    DeliveryStatus = "sent"
	DeliveryFailed  DeliveryStatus = "failed"
	DeliverySkipped DeliveryStatus = "skipped"
)

// Role is a user's authorization level.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// UserStatus is a user account's state.
type UserStatus string

const (
	UserActive  UserStatus = "active"
	UserBlocked UserStatus = "blocked"
)

// User is an authenticated account that owns channels and devices.
//
// Username is an optional, system-wide unique public handle used for display
// (e.g. on comments) — it is not a login credential; email remains the login.
// UsernameLower is the lowercased form persisted alongside it so uniqueness can
// be enforced case-insensitively (mirrors Channel.AliasLower). DisplayName, Bio,
// Phone and Website are optional profile/contact fields. AvatarID references a
// processed image in the blob store (served via the public GET /v1/images/{id}).
type User struct {
	ID            string     `bson:"_id,omitempty" json:"id"`
	Email         string     `bson:"email" json:"email"`
	PasswordHash  string     `bson:"passwordHash" json:"-"`
	Role          Role       `bson:"role" json:"role"`
	Status        UserStatus `bson:"status" json:"status"`
	Username      string     `bson:"username,omitempty" json:"username,omitempty"`
	UsernameLower string     `bson:"usernameLower,omitempty" json:"-"`
	DisplayName   string     `bson:"displayName,omitempty" json:"displayName,omitempty"`
	Bio           string     `bson:"bio,omitempty" json:"bio,omitempty"`
	Phone         string     `bson:"phone,omitempty" json:"phone,omitempty"`
	Website       string     `bson:"website,omitempty" json:"website,omitempty"`
	AvatarID      string     `bson:"avatarId,omitempty" json:"avatarId,omitempty"`
	CreatedAt     time.Time  `bson:"createdAt" json:"createdAt"`
}

// UserProfileUpdate carries the editable profile fields for UpdateUserProfile.
// Username is the canonical (display-cased) handle; an empty Username clears it.
// The store derives and persists the lowercased uniqueness key.
type UserProfileUpdate struct {
	Username    string
	DisplayName string
	Bio         string
	Phone       string
	Website     string
}

// PublicUser is the non-sensitive view of a user safe to expose to other
// members (e.g. as a comment author). It never includes the email.
type PublicUser struct {
	ID          string `json:"id"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	AvatarID    string `json:"avatarId,omitempty"`
}

// Public returns the PublicUser projection of u.
func (u User) Public() PublicUser {
	return PublicUser{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, AvatarID: u.AvatarID}
}

// ChannelStatus controls whether a channel accepts and delivers notifications.
type ChannelStatus string

const (
	ChannelActive   ChannelStatus = "active"
	ChannelDisabled ChannelStatus = "disabled"
)

// Channel is a named notification target with a public trigger ID and an
// optional public alias ("phetag") used to share and join it.
type Channel struct {
	ID       string `bson:"_id,omitempty" json:"id"`
	PublicID string `bson:"publicId" json:"publicId"`
	OwnerID  string `bson:"ownerId" json:"ownerId"`
	Name     string `bson:"name" json:"name"`
	// Alias is the human-facing, shareable handle ("phetag"), e.g. "skg_news".
	// Empty when unset. AliasLower is the lowercased form persisted alongside it
	// so uniqueness can be enforced case-insensitively (mirrors Device.WebPushEndpoint).
	Alias      string `bson:"alias,omitempty" json:"alias,omitempty"`
	AliasLower string `bson:"aliasLower,omitempty" json:"-"`
	// AvatarID references a processed image blob, served from /v1/images/{id}.
	// Empty when the channel has no picture, in which case clients fall back to a
	// generated colour and initials.
	AvatarID         string           `bson:"avatarId,omitempty" json:"avatarId,omitempty"`
	SubscriptionMode SubscriptionMode `bson:"subscriptionMode" json:"subscriptionMode"`
	Status           ChannelStatus    `bson:"status" json:"status"`
	CreatedAt        time.Time        `bson:"createdAt" json:"createdAt"`
}

// MemberStatus is the lifecycle state of a user's membership in a channel. It
// mirrors SubscriptionStatus but is kept distinct: membership is the per-user
// authority record (approval/ban/role), while a Subscription is per-device and
// drives push delivery.
type MemberStatus string

const (
	MemberActive  MemberStatus = "active"
	MemberPending MemberStatus = "pending"
	MemberBlocked MemberStatus = "blocked"
)

// ChannelMember is a user's membership in a channel: the per-channel role and
// status used for approvals, bans, and moderation. The channel owner is the
// implicit top authority and is not represented by a member row. Role reuses the
// Role type but is a per-channel grant, distinct from the global User.Role.
type ChannelMember struct {
	ID        string       `bson:"_id,omitempty" json:"id"`
	ChannelID string       `bson:"channelId" json:"channelId"`
	UserID    string       `bson:"userId" json:"userId"`
	Role      Role         `bson:"role" json:"role"`
	Status    MemberStatus `bson:"status" json:"status"`
	CreatedAt time.Time    `bson:"createdAt" json:"createdAt"`
}

// APIKey authenticates ingest requests for a channel. Only the hash is stored.
type APIKey struct {
	ID        string     `bson:"_id,omitempty" json:"id"`
	ChannelID string     `bson:"channelId" json:"channelId"`
	HashedKey string     `bson:"hashedKey" json:"-"`
	Prefix    string     `bson:"prefix" json:"prefix"`
	Label     string     `bson:"label" json:"label"`
	CreatedAt time.Time  `bson:"createdAt" json:"createdAt"`
	RevokedAt *time.Time `bson:"revokedAt,omitempty" json:"revokedAt,omitempty"`
}

// Device is a push target registered by a user.
type Device struct {
	ID         string   `bson:"_id,omitempty" json:"id"`
	UserID     string   `bson:"userId" json:"userId"`
	Platform   Platform `bson:"platform" json:"platform"`
	FCMToken   string   `bson:"fcmToken,omitempty" json:"fcmToken,omitempty"`
	WebPushSub string   `bson:"webPushSub,omitempty" json:"webPushSub,omitempty"`
	// VoIPToken is an iOS PushKit token, and it is NOT the FCM token — it is a different token, for a
	// different APNs topic (<bundle>.voip), delivered by a different push type. FCM cannot send to it:
	// it has no way to learn a PushKit token, cannot override apns-topic, and does not support
	// apns-push-type: voip. So an iPhone that should ring while the app is asleep needs this, and a
	// direct APNs connection to use it (see push_apns_voip.go). Empty on every other platform.
	VoIPToken string `bson:"voipToken,omitempty" json:"voipToken,omitempty"`
	// WebPushEndpoint is the subscription's endpoint URL, stored separately so a
	// web device can be uniquely identified (and upserted) by it.
	WebPushEndpoint string    `bson:"webPushEndpoint,omitempty" json:"-"`
	CreatedAt       time.Time `bson:"createdAt" json:"createdAt"`
	LastSeenAt      time.Time `bson:"lastSeenAt" json:"lastSeenAt"`
}

// Subscription links a device to a channel.
type Subscription struct {
	ID        string             `bson:"_id,omitempty" json:"id"`
	ChannelID string             `bson:"channelId" json:"channelId"`
	DeviceID  string             `bson:"deviceId" json:"deviceId"`
	Status    SubscriptionStatus `bson:"status" json:"status"`
	CreatedAt time.Time          `bson:"createdAt" json:"createdAt"`
}

// MessageImage references a processed image stored in the blob store. Width and
// Height are the final pixel dimensions, letting clients reserve aspect ratio
// before the image loads (avoiding layout shift).
type MessageImage struct {
	ID     string `bson:"id" json:"id"`
	Width  int    `bson:"width" json:"width"`
	Height int    `bson:"height" json:"height"`
}

// Message is a persisted notification belonging to a channel. Images, when
// present, are shown before the text (Instagram-style). CommentsAllowed records
// whether members may comment on this message (decided per-message when sending;
// defaults to true).
type Message struct {
	ID              string            `bson:"_id,omitempty" json:"id"`
	ChannelID       string            `bson:"channelId" json:"channelId"`
	Title           string            `bson:"title" json:"title"`
	Body            string            `bson:"body" json:"body"`
	Images          []MessageImage    `bson:"images,omitempty" json:"images,omitempty"`
	Data            map[string]string `bson:"data,omitempty" json:"data,omitempty"`
	CommentsAllowed bool              `bson:"commentsAllowed" json:"commentsAllowed"`
	CreatedAt       time.Time         `bson:"createdAt" json:"createdAt"`
}

// Comment is a member's comment on a message. ChannelID is denormalized so
// deletes cascade by channel and admin moderation can resolve the channel
// without a message lookup.
type Comment struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	MessageID string    `bson:"messageId" json:"messageId"`
	ChannelID string    `bson:"channelId" json:"channelId"`
	UserID    string    `bson:"userId" json:"userId"`
	Body      string    `bson:"body" json:"body"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}

// Delivery records a push attempt to a single device for a message.
type Delivery struct {
	ID        string         `bson:"_id,omitempty" json:"id"`
	MessageID string         `bson:"messageId" json:"messageId"`
	DeviceID  string         `bson:"deviceId" json:"deviceId"`
	Status    DeliveryStatus `bson:"status" json:"status"`
	Error     string         `bson:"error,omitempty" json:"error,omitempty"`
	SentAt    time.Time      `bson:"sentAt" json:"sentAt"`
}

// NotifyTask is the payload enqueued on the broker for the dispatcher to process.
// Images carries already-processed blob references (ids + dimensions) only — image
// bytes are stored before enqueue, so the broker payload stays small.
type NotifyTask struct {
	ChannelID       string            `json:"channelId"`
	Title           string            `json:"title"`
	Body            string            `json:"body"`
	Images          []MessageImage    `json:"images,omitempty"`
	Data            map[string]string `json:"data,omitempty"`
	CommentsAllowed bool              `json:"commentsAllowed"`
	IdempotencyKey  string            `json:"idempotencyKey,omitempty"`
	EnqueuedAt      time.Time         `json:"enqueuedAt"`
}

// ChannelVolume reports a channel's message count, used for "top channels".
type ChannelVolume struct {
	ChannelID string `json:"channelId"`
	Name      string `json:"name"`
	Count     int64  `json:"count"`
}

// AdminStats is the system-wide overview shown on the admin dashboard.
type AdminStats struct {
	Users          int64           `json:"users"`
	Channels       int64           `json:"channels"`
	Messages       int64           `json:"messages"`
	Deliveries     int64           `json:"deliveries"`
	Devices        int64           `json:"devices"`
	TopChannels    []ChannelVolume `json:"topChannels"`
	RecentMessages []Message       `json:"recentMessages"`
}

// --- Conversations (direct + group chats) ------------------------------------
//
// Conversations are the private, member-to-member counterpart of channels.
// Unlike a channel — which is a broadcast target the server can read — a
// conversation carries opaque message content the server never interprets. The
// server is an MLS Delivery Service here: it stores membership and relays bytes.
// Message content is end-to-end encrypted by the clients (see the crypto plan);
// the store treats it as an opaque blob and never as text.

// ConversationKind distinguishes a two-person direct chat from a named group.
type ConversationKind string

const (
	// ConversationDirect is a 1-to-1 chat between exactly two users. There is at
	// most one direct conversation per unordered pair (enforced by DirectKey).
	ConversationDirect ConversationKind = "direct"
	// ConversationGroup is a named, multi-member chat.
	ConversationGroup ConversationKind = "group"
)

// Conversation is a private chat. Title and AvatarID apply to groups; a direct
// chat has neither and is labelled client-side from the other member.
type Conversation struct {
	ID        string           `bson:"_id,omitempty" json:"id"`
	Kind      ConversationKind `bson:"kind" json:"kind"`
	Title     string           `bson:"title,omitempty" json:"title,omitempty"`
	AvatarID  string           `bson:"avatarId,omitempty" json:"avatarId,omitempty"`
	CreatedBy string           `bson:"createdBy" json:"createdBy"`
	// DirectKey is the deduplication key for direct chats: the two member ids
	// sorted and joined, so the pair {a,b} maps to one conversation regardless of
	// who starts it. Empty for groups. Uniquely indexed (partial) in Mongo.
	DirectKey string    `bson:"directKey,omitempty" json:"-"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// The conversation's MLS group, once a member has established one. Inline, so the
	// compare-and-set that serialises Commits is a single atomic update on this one
	// document. See domain.MLSGroupState.
	MLS MLSGroupState `bson:",inline" json:"-"`
}

// ConversationMember is a user's membership in a conversation. Role reuses the
// Role type: a group creator is 'admin', everyone else 'user'. Direct chats have
// two 'user' members and no admin.
type ConversationMember struct {
	ID             string    `bson:"_id,omitempty" json:"id"`
	ConversationID string    `bson:"conversationId" json:"conversationId"`
	UserID         string    `bson:"userId" json:"userId"`
	Role           Role      `bson:"role" json:"role"`
	JoinedAt       time.Time `bson:"joinedAt" json:"joinedAt"`
	// ClearedAt is this member's private "clear history" watermark: messages at or
	// before it are hidden from THIS member's fetches, on all their devices, while
	// leaving the shared log — and every other member's view of it — untouched. Zero
	// means nothing cleared. It is per-member on purpose: the ciphertext is a single
	// shared row per message (one MLS group message), so deleting rows would erase the
	// conversation for everyone; a watermark clears only the caller's own history.
	ClearedAt time.Time `bson:"clearedAt,omitempty" json:"clearedAt,omitempty"`

	// DeliveredAt and ReadAt are this member's receipt watermarks: they have RECEIVED
	// every message up to DeliveredAt, and READ every message up to ReadAt. Both only
	// ever move forward.
	//
	// Watermarks rather than a row per message per member: messages are ordered by
	// createdAt, so "read up to T" already says everything about every message at or
	// before T. A sender's ticks are then a comparison — their message is delivered when
	// every OTHER member's DeliveredAt has reached it, and read when every other member's
	// ReadAt has. That is the "everyone has read it" rule, and it costs one number per
	// member instead of a table the size of the conversation times its membership.
	//
	// They start at JoinedAt, which is what keeps a new group member from holding back the
	// ticks on everything said before they arrived — messages they cannot decrypt anyway,
	// MLS giving a member no access to what came before them.
	DeliveredAt time.Time `bson:"deliveredAt,omitempty" json:"deliveredAt,omitempty"`
	ReadAt      time.Time `bson:"readAt,omitempty" json:"readAt,omitempty"`
}

// ConversationReceipt is one member's receipt watermarks moving, carried on the live
// stream so a sender's ticks change under their eyes rather than on the next fetch.
type ConversationReceipt struct {
	UserID      string    `json:"userId"`
	DeliveredAt time.Time `json:"deliveredAt,omitempty"`
	ReadAt      time.Time `json:"readAt,omitempty"`
}

// ChatMessage is one message in a conversation. Unlike the broadcast Message, it
// has a SenderID (a chat message is authored by a user, not by a channel) and
// its content is an opaque, client-encrypted Ciphertext the server never reads.
// ContentType lets clients tell an application message from an MLS control
// message (Commit/Welcome) that rides the same ordered log.
// Attachment binds an encrypted photo to the conversation it belongs to.
//
// It records nothing about the photo, because the server knows nothing about the photo. What it
// received was AES-GCM ciphertext sealed under a key that lives inside the MLS-encrypted message
// referencing it — so there is no width, no height, no content type and no filename here. Those are
// properties of the plaintext, and they travel with the key.
//
// The record exists for exactly two reasons: to authorise a download (this blob belongs to THAT
// conversation, so only its members may fetch it), and to know what to delete when the conversation
// goes.
type Attachment struct {
	// ID is the blob id, which is also the id the encrypted message refers to.
	ID             string    `bson:"_id,omitempty" json:"id"`
	ConversationID string    `bson:"conversationId" json:"conversationId"`
	// Size of the ciphertext. The one thing the server does learn.
	Size      int       `bson:"size" json:"size"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}

type ChatMessage struct {
	ID             string `bson:"_id,omitempty" json:"id"`
	ConversationID string `bson:"conversationId" json:"conversationId"`
	SenderID       string `bson:"senderId" json:"senderId"`
	// Ciphertext is opaque bytes: MLS ciphertext once E2EE is on, plaintext-JSON
	// in the interim. The server stores and relays it without interpretation.
	Ciphertext  []byte    `bson:"ciphertext" json:"ciphertext"`
	ContentType string    `bson:"contentType" json:"contentType"`
	CreatedAt   time.Time `bson:"createdAt" json:"createdAt"`
	// MLSEpoch is the group epoch a control message (Welcome, Commit) produced. Zero on
	// ordinary messages, which have no epoch of their own.
	//
	// It is what lets a member that has fallen behind — a phone that was off, a browser
	// that has not been opened in a week — ask for exactly the Commits it is missing and
	// no others. Without it, catching up means trawling the message log and hoping the
	// Commits are still inside the last page; a member that misses one can never decrypt
	// anything again.
	MLSEpoch int64 `bson:"mlsEpoch,omitempty" json:"mlsEpoch,omitempty"`
}

// The content types clients use for MLS protocol traffic. They mirror the MLS_*
// constants in web/src/lib/mls.ts. The server never interprets the bytes — it only
// needs to tell protocol traffic from something a human sent, so that it does not push
// a notification for it and can order a catch-up correctly.
const (
	// ContentTypeMLSWelcome admits new devices to the group.
	ContentTypeMLSWelcome = "application/mls-welcome"
	// ContentTypeMLSCommit advances every current member to the new epoch.
	ContentTypeMLSCommit = "application/mls-commit"
	// ContentTypeMLSDevice is "I am a member of this conversation and my device is not in
	// its group" — posted by a device that holds no group, so that a member who does hold
	// it adds this device. It carries no key material.
	ContentTypeMLSDevice = "application/mls-device"
	// ContentTypeMLSHistoryRequest is "I just joined this conversation and hold none of its
	// past; can a device that has it send it to me?" — posted by a freshly-joined device that
	// has no local transcript. It carries the requester's identity and epoch, no key material.
	ContentTypeMLSHistoryRequest = "application/mls-history-request"
	// ContentTypeMLSHistoryOffer answers a request: "the history is sealed and waiting at this
	// object id." The transcript itself never rides the message — it is sealed under a key
	// derived from the group (which the server cannot derive) and stored as a blob; this only
	// points at it. Carries the requester identity, the epoch the key was derived at, and the id.
	ContentTypeMLSHistoryOffer = "application/mls-history-offer"
	// ContentTypeCallEvent is the record a call leaves in the conversation when nobody
	// answered it. Unlike the types above it IS user-visible — it is the "missed call" in
	// the transcript — and it is encrypted like any other message, so the server knows only
	// that one exists, never what it says.
	//
	// It is here for one reason: so that the server does not push a notification for it. The
	// phone was already rung when the call came in, and buzzing it a second time to announce
	// that the call it just told you about was missed is not a notification, it is a nag.
	ContentTypeCallEvent = "application/pheme-call-event"
)

// MLSProtocolContentTypes is the protocol traffic that rides a conversation's ordered log
// without being part of its TRANSCRIPT: nobody wrote it and no client renders it. The feed
// excludes it (Store.ChatMessagesByConversation), so a page of 50 is 50 things people
// actually said rather than 50 rows of which some are invisible. The catch-up that does
// need this traffic reads it through MLSControlMessagesSince, by epoch.
//
// ContentTypeCallEvent is deliberately absent: a missed call IS part of the transcript.
var MLSProtocolContentTypes = []string{
	ContentTypeMLSWelcome,
	ContentTypeMLSCommit,
	ContentTypeMLSDevice,
	ContentTypeMLSHistoryRequest,
	ContentTypeMLSHistoryOffer,
}

// IsMLSProtocol reports whether a content type is MLS protocol traffic rather than
// something a person sent.
func IsMLSProtocol(contentType string) bool {
	switch contentType {
	case ContentTypeMLSWelcome, ContentTypeMLSCommit, ContentTypeMLSDevice,
		ContentTypeMLSHistoryRequest, ContentTypeMLSHistoryOffer:
		return true
	default:
		return false
	}
}

// MLSGroupState is a conversation's MLS group: which group it is, and how far
// along that group's history the server has accepted.
//
// The server is an untrusted Delivery Service and reads none of the key material —
// but it is the only party every member agrees on, so it is the only thing that can
// answer "which group is this conversation, and whose Commit came first?". Without
// an answer to the first, two devices of the same person each create their own group
// under the conversation's name and encrypt past each other. Without an answer to the
// second, two members Commit against the same epoch and the group forks in two.
type MLSGroupState struct {
	// GroupID is the opaque MLS group id, minted by whoever established the group.
	// Empty until then. It is set exactly once and never changes: a conversation's
	// group cannot be replaced, because replacing it destroys the key material for
	// every message ever sent to it.
	GroupID string `bson:"mlsGroupId,omitempty" json:"groupId"`
	// Epoch is the MLS epoch of the last Commit the server accepted. A member proposing
	// a Commit says which epoch it is based on; if that is not this one, they are behind
	// and their Commit is refused.
	Epoch int64 `bson:"mlsEpoch,omitempty" json:"epoch"`
	// PriorGroupIDs are the groups this conversation used to use, newest first.
	//
	// A conversation's group can die: every device that held it can lose its key material at
	// once — a browser cleared, an iOS PWA whose storage was evicted, and there is no rule
	// that says it cannot happen to both people in the same week. Nobody is left who can
	// admit anybody, because admission is a Commit and only a member of the group can make
	// one. Without a way out, that conversation is dead forever.
	//
	// The way out is to start a NEW group and remember the old one. Nothing is destroyed:
	// anyone who still holds an old group can still read everything that was said to it, and
	// a client decrypts a message against whichever of its groups the message belongs to. That
	// is what makes this safe to do automatically, and it is the difference between this and
	// the "rebuild the group" behaviour that used to wipe a conversation for everyone in it —
	// that one deleted the old group; this one keeps it.
	PriorGroupIDs []string `bson:"mlsPriorGroupIds,omitempty" json:"priorGroupIds,omitempty"`
}

// MLSGroupInfo is the signed snapshot a NON-MEMBER needs to join a group by external commit
// (RFC 9420 §11.2.1) — without a Welcome and without any member having to admit it.
//
// It is derived state, not history: a member re-exports it after every Commit, and the server keeps
// only the latest. A joiner builds its external commit against it; if a newer Commit has landed since,
// the ordinary compare-and-set refuses the join and the joiner refetches. So a stale GroupInfo is
// self-correcting, never wrong. It is kept OUT of MLSGroupState because it is large and only the
// handful of devices actually joining ever need it — every group-state read must not carry it.
type MLSGroupInfo struct {
	GroupID   string `bson:"mlsGroupInfoGroupId,omitempty"`
	Epoch     int64  `bson:"mlsGroupInfoEpoch,omitempty"`
	GroupInfo []byte `bson:"mlsGroupInfo,omitempty"`
}

// MLSKeyPackage is a single-use public MLS KeyPackage a user's device has
// published, for others to add that user to an encrypted group. The server is
// the MLS Delivery Service's key directory: it stores these public bytes and
// hands one out (deleting it) when someone starts a group with the user. It never
// holds any private key material.
type MLSKeyPackage struct {
	ID         string    `bson:"_id,omitempty" json:"id"`
	UserID     string    `bson:"userId" json:"userId"`
	DeviceID   string    `bson:"deviceId" json:"deviceId"`
	KeyPackage []byte    `bson:"keyPackage" json:"keyPackage"`
	CreatedAt  time.Time `bson:"createdAt" json:"createdAt"`
	// LastResort marks the one KeyPackage per device that is handed out but never
	// consumed. KeyPackages are otherwise single-use, which means anyone could
	// simply claim a user's entire stock in a loop and leave them unreachable —
	// nobody could start an encrypted chat with them until their device happened to
	// republish. RFC 9420 anticipates this with last-resort KeyPackages: reusing one
	// costs a little forward secrecy on that single join, which is a far better
	// trade than being un-messageable on demand by any stranger.
	LastResort bool `bson:"lastResort,omitempty" json:"lastResort,omitempty"`
}

// MLSDevice is one of a user's own devices, as the user sees it in "your devices":
// its client-minted MLS device id, a human label (e.g. "Chrome on macOS"), and when it
// was last active. It carries no key material — the KeyPackage directory holds that. It
// exists so a user can SEE and MANAGE the devices signed in to their account, which the
// per-conversation KeyPackage directory cannot answer (it is scoped to a conversation's
// members, not to "all of my devices").
type MLSDevice struct {
	ID       string `bson:"_id,omitempty" json:"-"`
	UserID   string `bson:"userId" json:"-"`
	DeviceID string `bson:"deviceId" json:"deviceId"`
	Label    string `bson:"label" json:"label"`
	// SessionID is the id of the auth session this device last authenticated with — the
	// `sid` claim in its JWT. It is what lets "terminate this device" revoke the right
	// login and no other: never sent to any client (there is nothing a client does with
	// another device's session id but harm), refreshed whenever the device republishes.
	SessionID  string    `bson:"sessionId,omitempty" json:"-"`
	CreatedAt  time.Time `bson:"createdAt" json:"createdAt"`
	LastSeenAt time.Time `bson:"lastSeenAt" json:"lastSeenAt"`
}

// RevokedSession records one auth session that has been terminated before its token
// would expire. Auth tokens are stateless — signature and expiry only — so revoking one
// needs an explicit deny list: the middleware refuses any token whose `sid` is listed
// here. Entries carry the token's own expiry so they can be reaped once the token they
// deny would have been rejected on expiry anyway.
type RevokedSession struct {
	ID        string    `bson:"_id,omitempty" json:"-"`
	SessionID string    `bson:"sessionId" json:"sessionId"`
	ExpiresAt time.Time `bson:"expiresAt" json:"expiresAt"`
}

// MLSKeyBackup is the encrypted backup of a device's MLS client state, sealed
// client-side under a key derived from the user's recovery passphrase. The server
// stores only this opaque ciphertext (plus the public salt/nonce needed to derive
// and open it); it never sees the passphrase, the derived key, or the plaintext
// state. One backup per user — the latest upload replaces the previous.
type MLSKeyBackup struct {
	ID       string `bson:"_id,omitempty" json:"id"`
	UserID   string `bson:"userId" json:"userId"`
	DeviceID string `bson:"deviceId" json:"deviceId"`
	// Salt and nonce are tiny and stay inline. The sealed ciphertext does NOT: it lives in
	// the blob store (GridFS in production), and this record keeps only its id. A Mongo
	// document caps at 16MB, and a whole chat history — even sealed and text-only — can pass
	// that; storing the blob inline made "no limitation" a lie. GridFS chunks with no such
	// ceiling, so the backup is now bounded only by policy, not by the document format.
	Salt             []byte `bson:"salt" json:"salt"`
	Nonce            []byte `bson:"nonce" json:"nonce"`
	CiphertextBlobID string `bson:"ciphertextBlobId" json:"ciphertextBlobId"`

	// The user's decrypted transcripts, sealed under the same passphrase with their own salt
	// and nonce, and their own blob. Optional — a backup from before transcripts rode along
	// has none. They matter because decryption is one-shot: the keys alone recover the
	// ability to TALK, but everything already read exists nowhere except the device's local
	// cache, and this is that cache's only way off the device.
	TranscriptSalt   []byte `bson:"transcriptSalt,omitempty" json:"transcriptSalt,omitempty"`
	TranscriptNonce  []byte `bson:"transcriptNonce,omitempty" json:"transcriptNonce,omitempty"`
	TranscriptBlobID string `bson:"transcriptBlobId,omitempty" json:"transcriptBlobId,omitempty"`

	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// DirectKey builds the unique deduplication key for a direct chat between two
// users: their ids sorted and joined, so {a,b} and {b,a} collide to one row.
func DirectKey(userA, userB string) string {
	if userA > userB {
		userA, userB = userB, userA
	}
	return userA + ":" + userB
}

// aliasPattern enforces the phetag charset and start-character rule. The length
// bound (2–24) is implied by the quantifier and re-checked in ValidateAlias for
// a clearer error message.
var aliasPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9._-]{1,23}$`)

// ErrInvalidAlias is returned by ValidateAlias when the alias is malformed.
var ErrInvalidAlias = errors.New(
	"alias must be 2–24 characters of letters, digits, '.', '-' or '_', not start with a digit, '.' or '-', and not use the reserved 'ch_' prefix")

// ValidateAlias checks a channel alias ("phetag"): 2–24 characters drawn from
// [a-zA-Z0-9._-], not starting with a digit, '.' or '-', and not using the
// reserved "ch_" prefix (the shape of an auto-generated public trigger ID, so
// aliases can never shadow that namespace).
func ValidateAlias(alias string) error {
	if len(alias) < 2 || len(alias) > 24 || !aliasPattern.MatchString(alias) {
		return ErrInvalidAlias
	}
	if strings.HasPrefix(strings.ToLower(alias), "ch_") {
		return ErrInvalidAlias
	}
	return nil
}

// usernamePattern enforces the username charset and start-character rule.
var usernamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]{2,29}$`)

// ErrInvalidUsername is returned by ValidateUsername when the username is malformed.
var ErrInvalidUsername = errors.New(
	"username must be 3–30 characters of letters, digits, '.' or '_', and not start with a digit or '.'")

// ValidateUsername checks a user handle: 3–30 characters drawn from [a-zA-Z0-9_.],
// not starting with a digit or '.'. It is display-only (not a login credential)
// but unique system-wide, case-insensitively.
func ValidateUsername(username string) error {
	if len(username) < 3 || len(username) > 30 || !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	return nil
}
