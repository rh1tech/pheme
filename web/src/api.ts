// Minimal Pheme App API client.
//
// Auth is a development placeholder using the X-User-Id header; replace with a
// JWT bearer token once the auth endpoints are implemented.

const BASE = import.meta.env.VITE_API_BASE ?? 'http://localhost:8080'
const DEV_USER = import.meta.env.VITE_DEV_USER ?? 'dev1'

export interface Channel {
  id: string
  publicId: string
  ownerId: string
  name: string
  subscriptionMode: 'open' | 'approval'
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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      'X-User-Id': DEV_USER,
      ...(init?.headers ?? {}),
    },
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`${res.status}: ${text}`)
  }
  return res.json() as Promise<T>
}

export const api = {
  listChannels: () =>
    request<{ channels: Channel[] }>('/v1/channels').then((r) => r.channels ?? []),

  createChannel: (name: string, subscriptionMode: 'open' | 'approval') =>
    request<Channel>('/v1/channels', {
      method: 'POST',
      body: JSON.stringify({ name, subscriptionMode }),
    }),

  createKey: (channelId: string) =>
    request<{ id: string; key: string; prefix: string }>(
      `/v1/channels/${channelId}/keys`,
      { method: 'POST' },
    ),

  listMessages: (channelId: string) =>
    request<{ messages: Message[]; nextCursor: string }>(
      `/v1/channels/${channelId}/messages`,
    ),
}
