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

// ErrEpochConflict is returned by AdvanceMLSGroup when the caller's Commit is based
// on an epoch the conversation has already moved past — another member's Commit got
// there first. The caller must apply that Commit and re-propose, never force its own:
// a Commit applied locally but rejected by the group forks the client off it for good.
var ErrEpochConflict = errors.New("mls epoch conflict")

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
	// SetConversationMemberRole changes a member's role (admin/user). ErrNotFound if
	// the user is not a member.
	SetConversationMemberRole(ctx context.Context, conversationID, userID string, role domain.Role) error
	// DeleteConversation removes a conversation and everything belonging to it —
	// membership rows and the message log. The content was opaque; none of it is read.
	DeleteConversation(ctx context.Context, conversationID string) error
	// AppendChatMessage stores one message in a conversation's ordered log.
	AppendChatMessage(ctx context.Context, m domain.ChatMessage) (domain.ChatMessage, error)
	// ChatMessagesByConversation returns messages newest-first with cursor
	// pagination (cursor is an exclusive anchor message id), mirroring
	// MessagesByChannel. There is no query parameter — content is opaque.
	ChatMessagesByConversation(ctx context.Context, conversationID, cursor string, limit int) ([]domain.ChatMessage, error)
	// LastChatMessagesByConversations returns the newest message of each given
	// conversation, keyed by conversation id, for chat-list ordering/preview.
	LastChatMessagesByConversations(ctx context.Context, conversationIDs []string) (map[string]domain.ChatMessage, error)

	// MLSGroupState returns the conversation's MLS group id and epoch. The group id is
	// empty until somebody establishes the group.
	MLSGroupState(ctx context.Context, conversationID string) (domain.MLSGroupState, error)
	// CommitMLSGroup is the compare-and-set that serialises MLS Commits — and, in the
	// same breath, relays the control messages that Commit consists of.
	//
	// An MLS group has exactly one history: two members who both Commit against epoch N
	// produce two incompatible epoch N+1s, and a member who applies the wrong one is
	// forked off the conversation permanently. Something has to pick a winner, and the
	// server — the one party every member agrees on — is it.
	//
	// It accepts the change only when baseEpoch matches the epoch the conversation is
	// actually at (or, for the very first Commit, when no group has been established at
	// all), advances to baseEpoch+1, and appends `msgs` — the Welcome and Commit. A
	// caller whose base is stale gets ErrEpochConflict with the CURRENT state, so it can
	// catch up and re-propose.
	//
	// The decision and the relay are one operation on purpose. Appending the messages
	// first would publish a Welcome from a Commit the group is about to refuse, and a
	// device that joined from it would land in a group with no other members in it.
	// Deciding first and failing to append would advance the epoch with no Commit for
	// anyone to apply, stranding every member an epoch behind.
	CommitMLSGroup(ctx context.Context, conversationID, groupID string, baseEpoch int64, msgs []domain.ChatMessage) (domain.MLSGroupState, []domain.ChatMessage, error)
	// ResetMLSGroup retires the conversation's current group so a new one can be established.
	//
	// For a group nobody holds any more: every device that had it lost its key material, so
	// there is no member left who can admit anybody, and the conversation is otherwise dead
	// forever. The retired group is REMEMBERED, not deleted — anyone who still holds it can
	// still read everything that was said to it — so this destroys nothing, which is what
	// makes it safe to do without asking.
	//
	// A no-op when no group is established.
	ResetMLSGroup(ctx context.Context, conversationID string) (domain.MLSGroupState, error)
	// MLSControlMessagesSince returns the Welcomes and Commits that carried the group
	// past `sinceEpoch`, OLDEST FIRST — the order they must be applied in.
	//
	// A member that has fallen behind cannot decrypt anything until it applies the
	// Commits it missed, and those may be far outside the page of history the client
	// loads. Asking by epoch makes catching up exact and bounded instead of a trawl.
	MLSControlMessagesSince(ctx context.Context, conversationID string, sinceEpoch int64) ([]domain.ChatMessage, error)

	// MLS key directory (public KeyPackages). The server only relays these.
	AddKeyPackages(ctx context.Context, packages []domain.MLSKeyPackage) error
	// DevicesWithKeyPackages lists, per user, the devices that have published
	// KeyPackages — WITHOUT consuming any.
	//
	// This is how a member works out which devices are missing from an MLS group: claiming
	// is destructive, so it cannot be used to ask "who is out there?". Every device of a
	// member needs its own leaf, so the answer has to be per device, never per user.
	DevicesWithKeyPackages(ctx context.Context, userIDs []string) (map[string][]string, error)
	// ClaimKeyPackage returns and removes one KeyPackage belonging to ONE DEVICE of a
	// user (they are single-use; the reusable last-resort one is returned without being
	// removed). ErrNotFound when that device has published none.
	//
	// Device-scoped, not user-scoped. A user-scoped claim hands back a package belonging
	// to whichever of the user's devices the store happened to find, so a group built
	// from it contains one arbitrary device of theirs and every other device is locked
	// out of the conversation.
	ClaimKeyPackage(ctx context.Context, userID, deviceID string) (domain.MLSKeyPackage, error)
	// CountKeyPackages reports how many SINGLE-USE packages a device has left, so a
	// client replenishes before running out. The last-resort package is excluded: it
	// is never consumed, so counting it would tell the client it has stock it does
	// not have.
	CountKeyPackages(ctx context.Context, userID, deviceID string) (int64, error)
	// HasLastResortKeyPackage reports whether a device has published its one reusable
	// package yet.
	HasLastResortKeyPackage(ctx context.Context, userID, deviceID string) (bool, error)
	// DeleteKeyPackages removes every KeyPackage a device has published. A device that
	// has lost the private halves (a wipe, a fresh identity) must call this, or the
	// stale public packages stay claimable and anyone added with one lands in a group
	// the device can never join.
	DeleteKeyPackages(ctx context.Context, userID, deviceID string) error

	// Encrypted key backup (opaque ciphertext, one per user). PutKeyBackup
	// upserts; GetKeyBackup returns ErrNotFound when the user has none.
	PutKeyBackup(ctx context.Context, backup domain.MLSKeyBackup) error
	GetKeyBackup(ctx context.Context, userID string) (domain.MLSKeyBackup, error)

	// Admin
	AdminStats(ctx context.Context, topN, recentN int) (domain.AdminStats, error)

	// Lifecycle
	Close(ctx context.Context) error
}
