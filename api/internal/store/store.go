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
	// SetChannelAvatar sets (or clears, when avatarID is "") a channel's avatar
	// blob id, removing the blob it replaces.
	SetChannelAvatar(ctx context.Context, channelID, avatarID string) (domain.Channel, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	AdminListUsers(ctx context.Context, query string, offset, limit int) ([]domain.User, int64, error)
	// SearchUsers finds users by a case-insensitive match on username or display
	// name, for the "start a chat with…" picker. Email is never matched (it is not
	// public), and the caller-supplied query is trusted to be pre-validated for a
	// minimum length by the handler to limit enumeration.
	SearchUsers(ctx context.Context, query string, limit int) ([]domain.User, error)
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
	// DevicesForUsers returns every device belonging to the given users. Chat push
	// targets conversation members directly — there is no per-channel subscription
	// to filter by, since being in the conversation is the subscription.
	DevicesForUsers(ctx context.Context, userIDs []string) ([]domain.Device, error)

	// Messages & deliveries
	CreateMessage(ctx context.Context, m domain.Message) (domain.Message, error)
	MessagesByChannel(ctx context.Context, channelID, cursor, query string, limit int) ([]domain.Message, error)
	MessageByID(ctx context.Context, id string) (domain.Message, error)
	// MessagesAround returns a window of messages centred on messageID, newest
	// first — roughly half the limit newer than it and half older, so a search hit
	// can be shown in its surrounding conversation rather than on its own.
	MessagesAround(ctx context.Context, channelID, messageID string, limit int) ([]domain.Message, error)
	// DeleteMessage removes a message together with everything that hangs off it:
	// its image blobs, its comments and its delivery records.
	DeleteMessage(ctx context.Context, id string) error
	// LastMessagesByChannels returns the newest message of each channel, keyed by
	// channel ID. It backs the chat list's message preview; channels without a
	// message are absent from the map.
	LastMessagesByChannels(ctx context.Context, channelIDs []string) (map[string]domain.Message, error)
	CreateDelivery(ctx context.Context, d domain.Delivery) (domain.Delivery, error)

	// Comments (members comment on a message; posted instantly).
	CreateComment(ctx context.Context, c domain.Comment) (domain.Comment, error)
	CommentByID(ctx context.Context, id string) (domain.Comment, error)
	// CommentsByMessage returns a message's comments newest-first with cursor
	// pagination (cursor is an exclusive anchor comment id).
	CommentsByMessage(ctx context.Context, messageID, cursor string, limit int) ([]domain.Comment, error)
	// CommentCountsByMessages returns the number of comments on each message,
	// keyed by message ID. Messages without comments are absent from the map.
	CommentCountsByMessages(ctx context.Context, messageIDs []string) (map[string]int64, error)
	DeleteComment(ctx context.Context, id string) error
	// AdminListComments returns comments newest-first with the total count; a
	// non-empty query matches the comment body (case-insensitive substring).
	AdminListComments(ctx context.Context, query string, offset, limit int) ([]domain.Comment, int64, error)

	// Conversations (private direct + group chats). Content is opaque to the
	// store — a ChatMessage's Ciphertext is never read, only stored and relayed.
	CreateConversation(ctx context.Context, c domain.Conversation, members []domain.ConversationMember) (domain.Conversation, error)
	ConversationByID(ctx context.Context, id string) (domain.Conversation, error)
	// ConversationByDirectKey finds the existing direct chat for a user pair, or
	// ErrNotFound. Used to dedupe before creating a new direct conversation.
	ConversationByDirectKey(ctx context.Context, directKey string) (domain.Conversation, error)
	// ConversationsForUser returns the conversations a user belongs to, newest
	// activity first (or creation time when a conversation has no messages).
	ConversationsForUser(ctx context.Context, userID string) ([]domain.Conversation, error)
	ConversationMembers(ctx context.Context, conversationID string) ([]domain.ConversationMember, error)
	// ConversationMembership returns a user's membership row, or ErrNotFound if
	// they are not a member (the authorization check for every conversation op).
	ConversationMembership(ctx context.Context, conversationID, userID string) (domain.ConversationMember, error)
	AddConversationMember(ctx context.Context, m domain.ConversationMember) (domain.ConversationMember, error)
	RemoveConversationMember(ctx context.Context, conversationID, userID string) error
	// AppendChatMessage stores one message in a conversation's ordered log.
	AppendChatMessage(ctx context.Context, m domain.ChatMessage) (domain.ChatMessage, error)
	// ChatMessagesByConversation returns messages newest-first with cursor
	// pagination (cursor is an exclusive anchor message id), mirroring
	// MessagesByChannel. There is no query parameter — content is opaque.
	ChatMessagesByConversation(ctx context.Context, conversationID, cursor string, limit int) ([]domain.ChatMessage, error)
	// LastChatMessagesByConversations returns the newest message of each given
	// conversation, keyed by conversation id, for chat-list ordering/preview.
	LastChatMessagesByConversations(ctx context.Context, conversationIDs []string) (map[string]domain.ChatMessage, error)

	// MLS key directory (public KeyPackages). The server only relays these.
	AddKeyPackages(ctx context.Context, packages []domain.MLSKeyPackage) error
	// ClaimKeyPackage returns and removes one of a user's KeyPackages (they are
	// single-use). ErrNotFound when the user has none left to claim.
	ClaimKeyPackage(ctx context.Context, userID string) (domain.MLSKeyPackage, error)
	// CountKeyPackages reports how many a device has left, so a client replenishes
	// before running out.
	CountKeyPackages(ctx context.Context, userID, deviceID string) (int64, error)

	// Encrypted key backup (opaque ciphertext, one per user). PutKeyBackup
	// upserts; GetKeyBackup returns ErrNotFound when the user has none.
	PutKeyBackup(ctx context.Context, backup domain.MLSKeyBackup) error
	GetKeyBackup(ctx context.Context, userID string) (domain.MLSKeyBackup, error)

	// Admin
	AdminStats(ctx context.Context, topN, recentN int) (domain.AdminStats, error)

	// Lifecycle
	Close(ctx context.Context) error
}
