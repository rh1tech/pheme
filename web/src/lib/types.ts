// Shared types mirroring the Pheme App API responses.

export type SubscriptionMode = 'open' | 'approval'
export type Platform = 'ios' | 'android' | 'web'
export type Role = 'user' | 'admin'

export interface TokenResponse {
  accessToken: string
  refreshToken: string
  userId: string
  role: Role
}

export interface Channel {
  id: string
  publicId: string
  ownerId: string
  name: string
  subscriptionMode: SubscriptionMode
  createdAt: string
}

export interface Message {
  id: string
  channelId: string
  title: string
  body: string
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
  createdAt: string
  channelCount: number
}

export interface AdminChannel extends Channel {
  ownerEmail: string
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
