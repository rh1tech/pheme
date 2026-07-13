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
}

// Live event delivered over the one per-user SSE stream. Either a channel
// broadcast (channelId + message) or a conversation message (conversationId +
// chatMessage) — distinguished by which id is present.
export interface LiveEvent {
  channelId?: string
  message?: Message
  conversationId?: string
  chatMessage?: ChatMessage
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
