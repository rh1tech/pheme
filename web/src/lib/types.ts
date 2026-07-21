// Shared types mirroring the Pheme App API responses.

export type SubscriptionMode = 'open' | 'approval'
export type Platform = 'ios' | 'android' | 'web'
export type Role = 'user' | 'admin'
export type UserStatus = 'active' | 'blocked'
export type ChannelStatus = 'active' | 'disabled'
// Per-channel membership role (distinct from the global user Role) and status.
export type ChannelRole = 'user' | 'admin'
export type MemberStatus = 'active' | 'pending' | 'blocked'

export interface TokenResponse {
  accessToken: string
  refreshToken: string
  userId: string
  role: Role
}

// How much a notification may reveal on this user's own lock screen, before they unlock.
//
// A property of the RECIPIENT, not the sender: it answers "what may someone glancing at my phone
// learn".
//
//   'preview' — the message itself. The server still cannot read it: it ships the ciphertext and
//               the service worker decrypts it before drawing the banner.
//   'sender'  — who sent it, and their picture, but not what they said.
//   'generic' — only that something arrived.
export type NotificationPrivacy = 'preview' | 'sender' | 'generic'

// The authenticated user's own account + profile (from GET/PATCH /v1/me).
export interface User {
  id: string
  email: string
  role: Role
  status: UserStatus
  username?: string
  displayName?: string
  bio?: string
  phone?: string
  website?: string
  avatarId?: string
  // Absent means the account PREDATES this setting, and resolves to 'sender' — what those
  // accounts did before it existed. New accounts get an explicit 'preview' at creation, so
  // absence never means "new".
  notificationPrivacy?: NotificationPrivacy
  createdAt: string
}

// The public, non-sensitive view of a user (e.g. a comment author). Never email.
export interface PublicUser {
  id: string
  username?: string
  displayName?: string
  avatarId?: string
}

// Returned by endpoints that email a verification/reset code instead of logging
// the user in (register, forgot-password).
export interface CodeSentResponse {
  status: string
}

// --- Conversations (private direct + group chats) ---

export type ConversationKind = 'direct' | 'group'

export interface ConversationMember {
  id: string
  conversationId: string
  userId: string
  role: ChannelRole
  joinedAt: string
  // Hydrated public profile of the member, for labelling and avatars.
  user: PublicUser
  /**
   * How far this member has got: they have RECEIVED every message up to deliveredSeq and READ
   * every message up to readSeq. Watermarks, not per-message state — messages are ordered by
   * their per-conversation `seq`, so "read up to N" already covers every message at or before N,
   * and the ticks on your own message are a comparison against the other members' (see
   * messageReceipt).
   *
   * Absent or 0 on a member who has not reported since joining.
   */
  deliveredSeq?: number
  readSeq?: number
  /**
   * The conversation `seq` when this member joined — the floor their watermarks start at. Absent
   * or 0 means they have been here from the start.
   */
  joinSeq?: number
}

// A chat message as it comes off the wire. `ciphertext` is base64 of opaque
// bytes the server never read — plaintext-JSON today, MLS ciphertext once E2EE
// is on. Decode it with lib/chatContent, never by hand.
export interface ChatMessage {
  id: string
  conversationId: string
  senderId: string
  ciphertext: string
  contentType: string
  createdAt: string
  /**
   * The per-conversation monotonic sequence the hub assigned this message. It is how messages
   * are ordered for read receipts (see ConversationMember). Older messages predating sequencing
   * carry 0 / undefined.
   */
  seq?: number
  /**
   * The MLS epoch a control message (Welcome, Commit) produced. Absent on ordinary
   * messages. It is what lets a device that has fallen behind ask for exactly the
   * Commits it is missing, and apply them in order.
   */
  mlsEpoch?: number
}

/**
 * A conversation's MLS group, as the server records it.
 *
 * `groupId` is empty until a member establishes the group. It is set once and never
 * replaced — replacing a group destroys the key material for every message ever sent to
 * it. `epoch` is the last Commit the server accepted; a member proposes a Commit against
 * it, and is refused if somebody else got there first.
 */
export interface MLSGroupState {
  groupId: string
  epoch: number
  /**
   * Groups this conversation used to use, newest first.
   *
   * A group can die: every device that held it can lose its key material at once. Nobody is
   * then left who can admit anybody, because admission is a Commit and only a member can make
   * one — so the conversation starts a new group. The old ones are kept, not deleted: anyone
   * who still holds one can still read everything that was said to it, and a message is
   * decrypted against whichever group it belongs to.
   */
  priorGroupIds?: string[]
}

/** One device of one user — the unit of MLS group membership. */
export interface MLSDeviceRef {
  userId: string
  deviceId: string
}

/** A claimed KeyPackage, addressed to the device it belongs to. */
export interface MLSClaimedKeyPackage extends MLSDeviceRef {
  keyPackage: string
}

/** One of the user's own devices, as shown in "your devices". */
export interface MLSDevice {
  deviceId: string
  label: string
  createdAt: string
  lastSeenAt: string
}

// A conversation's newest message, for chat-list ordering and preview.
export interface LastChatMessage {
  id: string
  senderId: string
  ciphertext: string
  contentType: string
  createdAt: string
}

export interface Conversation {
  id: string
  kind: ConversationKind
  title?: string
  avatarId?: string
  createdBy: string
  createdAt: string
  members: ConversationMember[]
  lastMessage?: LastChatMessage
}

// The newest message of a channel, reduced to what the chat list renders. Absent
// on a channel that has never been notified.
export interface LastMessage {
  id: string
  title: string
  body: string
  imageCount: number
  createdAt: string
}

export interface Channel {
  id: string
  publicId: string
  ownerId: string
  name: string
  alias?: string
  /** Processed image blob, served from /v1/images/{id}. Absent → generated avatar. */
  avatarId?: string
  subscriptionMode: SubscriptionMode
  status: ChannelStatus
  createdAt: string
  lastMessage?: LastMessage
}

// A channel the caller has joined, with their per-channel role and member status.
export interface JoinedChannel extends Channel {
  role: ChannelRole
  memberStatus: MemberStatus
}

// The caller's relationship to a channel (from GET /v1/channels/:id).
export interface ChannelRelation {
  channel: Channel
  isOwner: boolean
  role: ChannelRole
  status: MemberStatus | 'none'
}

// A channel subscriber (member), with the user's email for display.
export interface Member {
  id: string
  channelId: string
  userId: string
  email: string
  role: ChannelRole
  status: MemberStatus
  createdAt: string
}

// A processed image attached to a message. Width/height are the final pixel
// dimensions, used to reserve aspect ratio before the image loads.
export interface MessageImage {
  id: string
  width: number
  height: number
}

export interface Message {
  id: string
  channelId: string
  title: string
  body: string
  images?: MessageImage[]
  data?: Record<string, string>
  commentsAllowed: boolean
  createdAt: string
  // Absent on a message delivered over the live stream: it is brand new, so its
  // count is genuinely zero until the feed refetches.
  commentCount?: number
}

// A comment on a message, with its author's public profile.
export interface Comment {
  id: string
  messageId: string
  channelId: string
  userId: string
  body: string
  createdAt: string
  author: PublicUser
}

export interface CommentsPage {
  comments: Comment[]
  nextCursor: string
}

export interface Device {
  id: string
  userId: string
  platform: Platform
  fcmToken?: string
  webPushSub?: string
  createdAt: string
  lastSeenAt: string
}

export interface CreatedKey {
  id: string
  key: string
  prefix: string
  note: string
}

export interface ApiKey {
  id: string
  channelId: string
  prefix: string
  label: string
  createdAt: string
  revokedAt?: string
}

export interface MessagesPage {
  messages: Message[]
  nextCursor: string
}

export interface Meta {
  vapidPublicKey: string
  homeDomain?: string
}

// Live event delivered over the one per-user SSE stream. Either a channel
// broadcast (channelId + message) or a conversation message (conversationId +
// chatMessage) — distinguished by which id is present.
export interface LiveEvent {
  channelId?: string
  message?: Message
  conversationId?: string
  chatMessage?: ChatMessage
  /** The conversation was deleted; drop it from the list and leave it if open. */
  conversationDeleted?: boolean
  /**
   * A member's receipt watermarks moved: they have received (or read) up to here. Carries
   * conversationId. It says how far someone has got, never what they read.
   */
  receipt?: {
    userId: string
    deliveredSeq?: number
    readSeq?: number
  }
  /**
   * A voice call has a new signal — a NUDGE, not the signal itself.
   *
   * The live stream is allowed to drop events, and a dropped SDP answer is a call that
   * silently never connects. So the signal lives in an ordered mailbox the client reads from
   * a cursor, and losing this event costs a few hundred milliseconds rather than the call.
   */
  callSignal?: {
    callId: string
    seq: number
    /** Who sent it. A user's own other devices must not ring for a call they placed. */
    fromUserId: string
  }
}

// --- Admin types ---

export interface AdminUser {
  id: string
  email: string
  role: Role
  status: UserStatus
  createdAt: string
  channelCount: number
}

export interface AdminChannel extends Channel {
  ownerEmail: string
}

// A comment enriched for the admin moderation panel.
export interface AdminComment {
  id: string
  messageId: string
  channelId: string
  userId: string
  body: string
  createdAt: string
  authorEmail: string
  authorId: string
  channelName: string
  messageTitle: string
}

export interface Paged<T> {
  items: T[]
  total: number
  page: number
  limit: number
}

export interface AdminStats {
  users: number
  channels: number
  messages: number
  deliveries: number
  devices: number
  topChannels: { channelId: string; name: string; count: number }[]
  recentMessages: Message[]
}
