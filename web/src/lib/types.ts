// Shared types mirroring the Pheme App API responses.

export type SubscriptionMode = 'open' | 'approval'
export type Platform = 'ios' | 'android' | 'web'

export interface TokenResponse {
  accessToken: string
  refreshToken: string
  userId: string
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
