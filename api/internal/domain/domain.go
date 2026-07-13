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
}

// ChatMessage is one message in a conversation. Unlike the broadcast Message, it
// has a SenderID (a chat message is authored by a user, not by a channel) and
// its content is an opaque, client-encrypted Ciphertext the server never reads.
// ContentType lets clients tell an application message from an MLS control
// message (Commit/Welcome) that rides the same ordered log.
type ChatMessage struct {
	ID             string    `bson:"_id,omitempty" json:"id"`
	ConversationID string    `bson:"conversationId" json:"conversationId"`
	SenderID       string    `bson:"senderId" json:"senderId"`
	// Ciphertext is opaque bytes: MLS ciphertext once E2EE is on, plaintext-JSON
	// in the interim. The server stores and relays it without interpretation.
	Ciphertext  []byte    `bson:"ciphertext" json:"ciphertext"`
	ContentType string    `bson:"contentType" json:"contentType"`
	CreatedAt   time.Time `bson:"createdAt" json:"createdAt"`
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
