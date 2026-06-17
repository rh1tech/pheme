// Pheme App API client with bearer auth and transparent access-token refresh.

import {
  loadTokens,
  saveTokens,
  clearTokens,
  type Tokens,
} from './tokens'
import type {
  ApiKey,
  Channel,
  CreatedKey,
  Device,
  Meta,
  MessagesPage,
  Platform,
  SubscriptionMode,
  TokenResponse,
} from './types'

const BASE = import.meta.env.VITE_API_BASE ?? 'http://localhost:8080'

/** Raised when the session is no longer valid and the user must re-authenticate. */
export class AuthError extends Error {}

/** Raised for non-2xx API responses. */
export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

let onAuthFailure: (() => void) | null = null

/** Registers a callback invoked when the session expires (e.g. to redirect to login). */
export function setOnAuthFailure(cb: () => void): void {
  onAuthFailure = cb
}

interface RequestOptions {
  method?: string
  body?: unknown
  /** When true, do not attach the bearer token or attempt a refresh. */
  public?: boolean
}

async function rawFetch<T>(path: string, opts: RequestOptions, token?: string): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (token) headers.Authorization = `Bearer ${token}`

  const res = await fetch(`${BASE}${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })

  if (res.status === 204) return undefined as T
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    const message = data?.error?.message ?? res.statusText
    throw new ApiError(res.status, message)
  }
  return data as T
}

async function refresh(tokens: Tokens): Promise<string> {
  try {
    const next = await rawFetch<TokenResponse>(
      '/v1/auth/refresh',
      { method: 'POST', body: { refreshToken: tokens.refreshToken }, public: true },
    )
    saveTokens({ accessToken: next.accessToken, refreshToken: next.refreshToken })
    return next.accessToken
  } catch {
    clearTokens()
    onAuthFailure?.()
    throw new AuthError('session expired')
  }
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  if (opts.public) return rawFetch<T>(path, opts)

  const tokens = loadTokens()
  if (!tokens) {
    onAuthFailure?.()
    throw new AuthError('not authenticated')
  }

  try {
    return await rawFetch<T>(path, opts, tokens.accessToken)
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      const newAccess = await refresh(tokens)
      return rawFetch<T>(path, opts, newAccess)
    }
    throw err
  }
}

export const api = {
  // Auth (public)
  register: (email: string, password: string) =>
    request<TokenResponse>('/v1/auth/register', { method: 'POST', body: { email, password }, public: true }),
  login: (email: string, password: string) =>
    request<TokenResponse>('/v1/auth/login', { method: 'POST', body: { email, password }, public: true }),

  // Meta (public)
  meta: () => request<Meta>('/v1/meta', { public: true }),

  // Channels
  listChannels: () => request<{ channels: Channel[] }>('/v1/channels').then((r) => r.channels ?? []),
  createChannel: (name: string, subscriptionMode: SubscriptionMode) =>
    request<Channel>('/v1/channels', { method: 'POST', body: { name, subscriptionMode } }),
  updateChannel: (id: string, body: { name?: string; subscriptionMode?: SubscriptionMode }) =>
    request<Channel>(`/v1/channels/${id}`, { method: 'PATCH', body }),
  deleteChannel: (id: string) => request<void>(`/v1/channels/${id}`, { method: 'DELETE' }),

  // API keys
  createKey: (channelId: string) =>
    request<CreatedKey>(`/v1/channels/${channelId}/keys`, { method: 'POST' }),
  listKeys: (channelId: string) =>
    request<{ keys: ApiKey[] }>(`/v1/channels/${channelId}/keys`).then((r) => r.keys ?? []),
  revokeKey: (channelId: string, keyId: string) =>
    request<unknown>(`/v1/channels/${channelId}/keys/${keyId}`, { method: 'DELETE' }),

  // Send a message from the authenticated UI (owner only)
  notifyChannel: (channelId: string, title: string, body: string) =>
    request<unknown>(`/v1/channels/${channelId}/notify`, { method: 'POST', body: { title, body } }),

  // Devices & subscriptions
  createDevice: (body: { platform: Platform; fcmToken?: string; webPushSub?: string }) =>
    request<Device>('/v1/devices', { method: 'POST', body }),
  subscribe: (channelId: string, deviceId: string) =>
    request<unknown>(`/v1/channels/${channelId}/subscribe`, { method: 'POST', body: { deviceId } }),

  // Messages
  listMessages: (channelId: string, cursor = '', query = '', limit = 50) => {
    const q = new URLSearchParams()
    if (cursor) q.set('cursor', cursor)
    if (query) q.set('q', query)
    q.set('limit', String(limit))
    return request<MessagesPage>(`/v1/channels/${channelId}/messages?${q.toString()}`).then(
      (page) => ({ messages: page.messages ?? [], nextCursor: page.nextCursor ?? '' }),
    )
  },
}

/** Builds the SSE stream URL with the current access token as a query parameter. */
export function streamUrl(): string | null {
  const tokens = loadTokens()
  if (!tokens) return null
  return `${BASE}/v1/stream?token=${encodeURIComponent(tokens.accessToken)}`
}
