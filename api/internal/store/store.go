// Package store defines persistence contracts for Pheme and ships an in-memory
// implementation used for local development and tests.
//
// A MongoDB-backed implementation should satisfy the same Store interface; see
// store_mongo.go (TODO) for the production adapter.
package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// ErrAliasTaken is returned when setting a channel alias that another channel
// already uses (case-insensitively). Handlers map it to HTTP 409.
var ErrAliasTaken = errors.New("alias taken")

// ErrUsernameTaken is returned when setting a username that another user already
// uses (case-insensitively). Handlers map it to HTTP 409.
var ErrUsernameTaken = errors.New("username taken")

// deleteBlobs best-effort removes image blobs for cascade deletes. A nil store or
// a per-id failure is ignored: the history rows are already gone, so a leftover
// blob is at worst harmless garbage to be reclaimed later.
func deleteBlobs(ctx context.Context, blobs blob.Store, ids []string) {
	if blobs == nil || len(ids) == 0 {
		return
	}
	for _, id := range ids {
		_ = blobs.Delete(ctx, id)
	}
}

// webPushEndpoint extracts the endpoint URL from a Web Push subscription JSON
// string, or returns "" if the input is empty or not a web push subscription.
func webPushEndpoint(webPushSub string) string {
	if webPushSub == "" {
		return ""
	}
	var s struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal([]byte(webPushSub), &s); err != nil {
		return ""
	}
	return s.Endpoint
}

// Store is the persistence contract used by the App API and Dispatcher.
type Store interface {
	// Users
	CreateUser(ctx context.Context, u domain.User) (domain.User, error)
	UserByID(ctx context.Context, id string) (domain.User, error)
	UserByEmail(ctx context.Context, email string) (domain.User, error)
	// UserByUsername looks up a user by the lowercased username key.
	UserByUsername(ctx context.Context, usernameLower string) (domain.User, error)
	// UsersByIDs returns the requested users keyed by id (missing ids are omitted).
	UsersByIDs(ctx context.Context, ids []string) (map[string]domain.User, error)
	UpdateUserRole(ctx context.Context, userID string, role domain.Role) error
	UpdateUserStatus(ctx context.Context, userID string, status domain.UserStatus) error
	UpdateUserPassword(ctx context.Context, userID, passwordHash string) error
	// UpdateUserProfile sets the editable profile fields. An empty Username clears
	// it. Returns ErrUsernameTaken if another user already uses the username.
	UpdateUserProfile(ctx context.Context, userID string, p domain.UserProfileUpdate) (domain.User, error)
	// SetUserAvatar sets (or clears, when avatarID is "") a user's avatar blob id,
	// deleting any previously referenced blob.
	SetUserAvatar(ctx context.Context, userID, avatarID string) (domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	AdminListUsers(ctx context.Context, query string, offset, limit int) ([]domain.User, int64, error)
	DeleteUser(ctx context.Context, userID string) error

	// Channels
	CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error)
	ChannelByID(ctx context.Context, id string) (domain.Channel, error)
	ChannelByPublicID(ctx context.Context, publicID string) (domain.Channel, error)
	ChannelByAlias(ctx context.Context, aliasLower string) (domain.Channel, error)
	ChannelsByOwner(ctx context.Context, ownerID string) ([]domain.Channel, error)
	UpdateChannel(ctx context.Context, id, name string, mode domain.SubscriptionMode) (domain.Channel, error)
	// SetChannelAlias sets (or clears, when alias is "") a channel's phetag.
	// Returns ErrAliasTaken if another channel already uses it case-insensitively.
	SetChannelAlias(ctx context.Context, channelID, alias string) (domain.Channel, error)
	UpdateChannelStatus(ctx context.Context, id string, status domain.ChannelStatus) (domain.Channel, error)
	DeleteChannel(ctx context.Context, id string) error
	ListAllChannels(ctx context.Context) ([]domain.Channel, error)
	AdminListChannels(ctx context.Context, query string, offset, limit int) ([]domain.Channel, int64, error)

	// Channel members (per-user membership: approvals, bans, per-channel roles).
	// The channel owner is the implicit top authority and has no member row.
	UpsertMember(ctx context.Context, m domain.ChannelMember) (domain.ChannelMember, error)
	MembershipForUser(ctx context.Context, channelID, userID string) (domain.ChannelMember, error)
	// ListMembers returns a channel's members newest-first with the total count.
	// A non-empty status filters to that status (e.g. pending = approvals queue).
	ListMembers(ctx context.Context, channelID string, status domain.MemberStatus, offset, limit int) ([]domain.ChannelMember, int64, error)
	UpdateMemberStatus(ctx context.Context, channelID, userID string, status domain.MemberStatus) error
	UpdateMemberRole(ctx context.Context, channelID, userID string, role domain.Role) error
	RemoveMember(ctx context.Context, channelID, userID string) error
	ChannelsForMember(ctx context.Context, userID string) ([]domain.Channel, error)

	// API keys
	CreateAPIKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error)
	APIKeysByChannel(ctx context.Context, channelID string) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, keyID string) error

	// Devices & subscriptions
	CreateDevice(ctx context.Context, d domain.Device) (domain.Device, error)
	Subscribe(ctx context.Context, s domain.Subscription) (domain.Subscription, error)
	SubscriptionForDevice(ctx context.Context, channelID, deviceID string) (domain.Subscription, error)
	Unsubscribe(ctx context.Context, channelID, deviceID string) error
	// SetSubscriptionStatusForUser flips the status of every subscription the
	// user's devices have in the channel. It is how a membership approval/ban
	// propagates to push delivery (no-op when the user has no device subscriptions).
	SetSubscriptionStatusForUser(ctx context.Context, channelID, userID string, status domain.SubscriptionStatus) error
	ActiveDevicesForChannel(ctx context.Context, channelID string) ([]domain.Device, error)

	// Messages & deliveries
	CreateMessage(ctx context.Context, m domain.Message) (domain.Message, error)
	MessagesByChannel(ctx context.Context, channelID, cursor, query string, limit int) ([]domain.Message, error)
	MessageByID(ctx context.Context, id string) (domain.Message, error)
	CreateDelivery(ctx context.Context, d domain.Delivery) (domain.Delivery, error)

	// Comments (members comment on a message; posted instantly).
	CreateComment(ctx context.Context, c domain.Comment) (domain.Comment, error)
	CommentByID(ctx context.Context, id string) (domain.Comment, error)
	// CommentsByMessage returns a message's comments newest-first with cursor
	// pagination (cursor is an exclusive anchor comment id).
	CommentsByMessage(ctx context.Context, messageID, cursor string, limit int) ([]domain.Comment, error)
	DeleteComment(ctx context.Context, id string) error
	// AdminListComments returns comments newest-first with the total count; a
	// non-empty query matches the comment body (case-insensitive substring).
	AdminListComments(ctx context.Context, query string, offset, limit int) ([]domain.Comment, int64, error)

	// Admin
	AdminStats(ctx context.Context, topN, recentN int) (domain.AdminStats, error)

	// Lifecycle
	Close(ctx context.Context) error
}
