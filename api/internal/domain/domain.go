// Package domain defines the core Pheme entities shared across services.
package domain

import "time"

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
type User struct {
	ID           string     `bson:"_id,omitempty" json:"id"`
	Email        string     `bson:"email" json:"email"`
	PasswordHash string     `bson:"passwordHash" json:"-"`
	Role         Role       `bson:"role" json:"role"`
	Status       UserStatus `bson:"status" json:"status"`
	CreatedAt    time.Time  `bson:"createdAt" json:"createdAt"`
}

// ChannelStatus controls whether a channel accepts and delivers notifications.
type ChannelStatus string

const (
	ChannelActive   ChannelStatus = "active"
	ChannelDisabled ChannelStatus = "disabled"
)

// Channel is a named notification target with a public trigger ID.
type Channel struct {
	ID               string           `bson:"_id,omitempty" json:"id"`
	PublicID         string           `bson:"publicId" json:"publicId"`
	OwnerID          string           `bson:"ownerId" json:"ownerId"`
	Name             string           `bson:"name" json:"name"`
	SubscriptionMode SubscriptionMode `bson:"subscriptionMode" json:"subscriptionMode"`
	Status           ChannelStatus    `bson:"status" json:"status"`
	CreatedAt        time.Time        `bson:"createdAt" json:"createdAt"`
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
	ID         string    `bson:"_id,omitempty" json:"id"`
	UserID     string    `bson:"userId" json:"userId"`
	Platform   Platform  `bson:"platform" json:"platform"`
	FCMToken   string    `bson:"fcmToken,omitempty" json:"fcmToken,omitempty"`
	WebPushSub string    `bson:"webPushSub,omitempty" json:"webPushSub,omitempty"`
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

// Message is a persisted notification belonging to a channel.
type Message struct {
	ID        string            `bson:"_id,omitempty" json:"id"`
	ChannelID string            `bson:"channelId" json:"channelId"`
	Title     string            `bson:"title" json:"title"`
	Body      string            `bson:"body" json:"body"`
	Data      map[string]string `bson:"data,omitempty" json:"data,omitempty"`
	CreatedAt time.Time         `bson:"createdAt" json:"createdAt"`
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
type NotifyTask struct {
	ChannelID      string            `json:"channelId"`
	Title          string            `json:"title"`
	Body           string            `json:"body"`
	Data           map[string]string `json:"data,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey,omitempty"`
	EnqueuedAt     time.Time         `json:"enqueuedAt"`
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
