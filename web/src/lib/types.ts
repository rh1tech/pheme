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

// Returned by endpoints that email a verification/reset code instead of logging
// the user in (register, forgot-password).
export interface CodeSentResponse {
  status: string
}

export interface Channel {
  id: string
  publicId: string
  ownerId: string
  name: string
  alias?: string
  subscriptionMode: SubscriptionMode
  status: ChannelStatus
  createdAt: string
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
  createdAt: string
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

// Live event delivered over the SSE stream.
export interface LiveEvent {
  channelId: string
  message: Message
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
