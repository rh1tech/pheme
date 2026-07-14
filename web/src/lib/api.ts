// Pheme App API client with bearer auth and transparent access-token refresh.

import { nprogress } from '@mantine/nprogress'
import {
  loadTokens,
  saveTokens,
  clearTokens,
  type Tokens,
} from './tokens'
import type {
  AdminChannel,
  AdminComment,
  AdminStats,
  AdminUser,
  ApiKey,
  Channel,
  ChannelRelation,
  ChannelRole,
  ChannelStatus,
  ChatMessage,
  CodeSentResponse,
  Conversation,
  ConversationMember,
  Comment,
  CommentsPage,
  CreatedKey,
  Device,
  JoinedChannel,
  Member,
  MemberStatus,
  Message,
  Meta,
  MessagesPage,
  MLSClaimedKeyPackage,
  MLSDeviceRef,
  MLSGroupState,
  Platform,
  PublicUser,
  Role,
  SubscriptionMode,
  TokenResponse,
  User,
  UserStatus,
} from './types'

// Resolve the API base URL: runtime config (production container) first, then
// the build-time Vite env (local dev), then a localhost default.
const runtimeApiBase =
  typeof window !== 'undefined' ? window.__PHEME_CONFIG?.apiBase : undefined
const BASE = runtimeApiBase || import.meta.env.VITE_API_BASE || 'http://localhost:8080'

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
  /**
   * When true, skip the global top progress bar. Used by background fetches the
   * reader did not ask for — paginating older messages while scrolling would
   * otherwise flash the bar on every page.
   */
  quiet?: boolean
  /** When true, a 404 resolves to null instead of throwing — for "maybe absent"
   *  resources the caller wants to probe (e.g. an optional key backup). */
  allow404?: boolean
}

async function rawFetch<T>(path: string, opts: RequestOptions, token?: string): Promise<T> {
  const isForm = opts.body instanceof FormData
  const headers: Record<string, string> = {}
  // For FormData the browser sets Content-Type (with the multipart boundary);
  // setting it manually would break the upload.
  if (!isForm) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`

  const res = await fetch(`${BASE}${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body:
      opts.body === undefined ? undefined : isForm ? (opts.body as FormData) : JSON.stringify(opts.body),
  })

  if (res.status === 204) return undefined as T
  if (res.status === 404 && opts.allow404) return null as T
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

// A top loading bar is shown while any API request is in flight. A counter keeps
// it visible until the last concurrent request finishes.
//
// The bar is delayed rather than shown immediately: most requests here finish in
// tens of milliseconds, and a bar that appears and vanishes within a frame or two
// reads as a glitch, not as progress. Nothing is drawn unless the work outlasts
// this delay — so a fast channel switch is silent, and only a genuinely slow one
// announces itself.
const PROGRESS_DELAY_MS = 400

let inflight = 0
let showTimer: ReturnType<typeof setTimeout> | null = null
let showing = false

function progressStart(): void {
  inflight++
  if (inflight > 1 || showTimer !== null || showing) return
  showTimer = setTimeout(() => {
    showTimer = null
    // Still waiting on something when the delay elapsed — now the bar earns its keep.
    if (inflight === 0) return
    nprogress.start()
    showing = true
  }, PROGRESS_DELAY_MS)
}

function progressDone(): void {
  inflight = Math.max(0, inflight - 1)
  if (inflight > 0) return

  if (showTimer !== null) {
    clearTimeout(showTimer)
    showTimer = null
  }
  // Only complete a bar that was actually started; completing an unstarted
  // nprogress flashes it to 100% — the very flicker this delay exists to avoid.
  if (showing) {
    nprogress.complete()
    showing = false
  }
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  if (!opts.quiet) progressStart()
  try {
    if (opts.public) return await rawFetch<T>(path, opts)

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
        return await rawFetch<T>(path, opts, newAccess)
      }
      throw err
    }
  } finally {
    if (!opts.quiet) progressDone()
  }
}

export const api = {
  // Auth (public)
  register: (email: string, password: string) =>
    request<CodeSentResponse>('/v1/auth/register', { method: 'POST', body: { email, password }, public: true }),
  verifyEmail: (email: string, code: string) =>
    request<TokenResponse>('/v1/auth/verify', { method: 'POST', body: { email, code }, public: true }),
  login: (email: string, password: string) =>
    request<TokenResponse>('/v1/auth/login', { method: 'POST', body: { email, password }, public: true }),
  forgotPassword: (email: string) =>
    request<CodeSentResponse>('/v1/auth/forgot-password', { method: 'POST', body: { email }, public: true }),
  resetPassword: (email: string, code: string, newPassword: string) =>
    request<TokenResponse>('/v1/auth/reset-password', {
      method: 'POST',
      body: { email, code, newPassword },
      public: true,
    }),

  // Meta (public)
  meta: () => request<Meta>('/v1/meta', { public: true }),

  // Profile (self)
  getMe: () => request<User>('/v1/me'),
  updateMe: (body: {
    username?: string
    displayName?: string
    bio?: string
    phone?: string
    website?: string
  }) => request<User>('/v1/me', { method: 'PATCH', body }),
  uploadAvatar: (file: File) => {
    const form = new FormData()
    form.set('avatar', file)
    return request<User>('/v1/me/avatar', { method: 'POST', body: form })
  },
  deleteAvatar: () => request<User>('/v1/me/avatar', { method: 'DELETE' }),

  // Channels
  listChannels: () => request<{ channels: Channel[] }>('/v1/channels').then((r) => r.channels ?? []),
  getChannel: (id: string) => request<ChannelRelation>(`/v1/channels/${id}`),
  createChannel: (name: string, subscriptionMode: SubscriptionMode) =>
    request<Channel>('/v1/channels', { method: 'POST', body: { name, subscriptionMode } }),
  updateChannel: (id: string, body: { name?: string; subscriptionMode?: SubscriptionMode; alias?: string }) =>
    request<Channel>(`/v1/channels/${id}`, { method: 'PATCH', body }),
  deleteChannel: (id: string) => request<void>(`/v1/channels/${id}`, { method: 'DELETE' }),
  uploadChannelAvatar: (id: string, file: File) => {
    const form = new FormData()
    form.set('avatar', file)
    return request<Channel>(`/v1/channels/${id}/avatar`, { method: 'POST', body: form })
  },
  deleteChannelAvatar: (id: string) =>
    request<Channel>(`/v1/channels/${id}/avatar`, { method: 'DELETE' }),

  // MLS key directory. KeyPackages cross as base64 (Go []byte).
  publishKeyPackages: (deviceId: string, keyPackages: string[], lastResortKeyPackage?: string) =>
    request<void>('/v1/mls/key-packages', {
      method: 'POST',
      body: { deviceId, keyPackages, lastResortKeyPackage },
    }),
  /** `count` is the single-use stock; the last-resort package is never consumed. */
  keyPackageCount: (deviceId: string) =>
    request<{ count: number; hasLastResort: boolean }>(
      `/v1/mls/key-packages/count?deviceId=${encodeURIComponent(deviceId)}`,
    ),
  /** Purges this device's published key packages (used when minting a fresh identity). */
  deleteKeyPackages: (deviceId: string) =>
    request<void>(`/v1/mls/key-packages?deviceId=${encodeURIComponent(deviceId)}`, {
      method: 'DELETE',
    }),
  /**
   * Which devices each user has published keys for. Consumes nothing.
   *
   * A member needs this to work out which devices are MISSING from a group — every
   * device of a member is its own MLS leaf, and one that is not in the group cannot read
   * a word of the conversation. It cannot be answered by claiming, because claiming
   * destroys what it hands back.
   */
  mlsDevices: (conversationId: string) =>
    request<{ devices: Record<string, string[]> }>(
      `/v1/conversations/${conversationId}/mls/devices`,
    ).then((r) => r.devices ?? {}),

  /**
   * Claims one KeyPackage per named DEVICE, so each can be added to a group as its own
   * leaf. Devices that have published nothing are simply absent from the result; a 404
   * means none of them were reachable.
   */
  claimKeyPackages: (conversationId: string, devices: MLSDeviceRef[]) =>
    request<{ keyPackages: MLSClaimedKeyPackage[] }>(
      `/v1/conversations/${conversationId}/mls/key-packages/claim`,
      { method: 'POST', body: { devices } },
    ).then((r) => r.keyPackages ?? []),

  /** The conversation's MLS group id and epoch. `groupId` is empty until it is established. */
  mlsGroupState: (conversationId: string) =>
    request<MLSGroupState>(`/v1/conversations/${conversationId}/mls`),

  /**
   * The Welcomes and Commits that carried the group past `since`, oldest first — what a
   * member holding an older epoch must apply, in the order it must apply them.
   */
  mlsCommitsSince: (conversationId: string, since: number) =>
    request<{ messages: ChatMessage[] }>(
      `/v1/conversations/${conversationId}/mls/commits?since=${since}`,
    ).then((r) => r.messages ?? []),

  /**
   * Proposes a membership Commit, and relays it if the group is still at `baseEpoch`.
   *
   * A 409 means another member's Commit landed first, so this one is built on a history
   * that never happened. The caller MUST throw its Commit away rather than apply it —
   * applying a Commit the group refused forks this device off the conversation for good
   * — then catch up and propose again.
   */
  mlsCommit: (
    conversationId: string,
    body: {
      groupId: string
      baseEpoch: number
      welcome?: string
      commit: string
      /** The users whose leaves this Commit removes — see the server's mayRemove. */
      removes?: string[]
    },
  ) =>
    request<MLSGroupState>(`/v1/conversations/${conversationId}/mls/commit`, {
      method: 'POST',
      body,
    }),

  // Encrypted key backup. All fields are base64 of opaque bytes; the server never
  // sees the passphrase or the plaintext state.
  putKeyBackup: (deviceId: string, salt: string, nonce: string, ciphertext: string) =>
    request<void>('/v1/mls/key-backup', {
      method: 'PUT',
      body: { deviceId, salt, nonce, ciphertext },
    }),
  getKeyBackup: (quiet = false) =>
    request<{ salt: string; nonce: string; ciphertext: string; updatedAt: string } | null>(
      '/v1/mls/key-backup',
      { quiet, allow404: true },
    ),

  // User search for starting a chat (public profiles only, never email).
  searchUsers: (q: string) =>
    request<{ users: PublicUser[] }>(`/v1/users/search?q=${encodeURIComponent(q)}`).then(
      (r) => r.users ?? [],
    ),

  // Membership: join by trigger ID or phetag, the caller's joined channels, and leaving.
  joinChannel: (ref: string, deviceId?: string) =>
    request<{ channel: Channel }>('/v1/channels/join', { method: 'POST', body: { ref, deviceId } }),
  listJoinedChannels: () =>
    request<{ channels: JoinedChannel[] }>('/v1/channels/joined').then((r) => r.channels ?? []),
  leaveChannel: (id: string) => request<void>(`/v1/channels/${id}/membership`, { method: 'DELETE' }),

  // Approvals & subscriber management (owner / channel-admin).
  listApprovals: (channelId: string) =>
    request<{ members: Member[]; total: number }>(`/v1/channels/${channelId}/approvals`).then(
      (r) => r.members ?? [],
    ),
  approveMember: (channelId: string, userId: string) =>
    request<unknown>(`/v1/channels/${channelId}/approvals/${userId}`, { method: 'POST' }),
  denyMember: (channelId: string, userId: string) =>
    request<void>(`/v1/channels/${channelId}/approvals/${userId}`, { method: 'DELETE' }),
  listMembers: (channelId: string, offset = 0, limit = 50) => {
    const p = new URLSearchParams({ offset: String(offset), limit: String(limit) })
    return request<{ members: Member[]; total: number; offset: number; limit: number }>(
      `/v1/channels/${channelId}/members?${p.toString()}`,
    ).then((r) => ({ items: r.members ?? [], total: r.total, offset: r.offset, limit: r.limit }))
  },
  updateMember: (channelId: string, userId: string, body: { role?: ChannelRole; status?: MemberStatus }) =>
    request<unknown>(`/v1/channels/${channelId}/members/${userId}`, { method: 'PATCH', body }),
  removeMember: (channelId: string, userId: string) =>
    request<void>(`/v1/channels/${channelId}/members/${userId}`, { method: 'DELETE' }),

  // API keys
  createKey: (channelId: string) =>
    request<CreatedKey>(`/v1/channels/${channelId}/keys`, { method: 'POST' }),
  listKeys: (channelId: string) =>
    request<{ keys: ApiKey[] }>(`/v1/channels/${channelId}/keys`).then((r) => r.keys ?? []),
  revokeKey: (channelId: string, keyId: string) =>
    request<unknown>(`/v1/channels/${channelId}/keys/${keyId}`, { method: 'DELETE' }),

  // Send a message from the authenticated UI (owner only). With images, the body
  // is sent as multipart/form-data; text-only sends stay JSON.
  notifyChannel: (
    channelId: string,
    title: string,
    body: string,
    images: File[] = [],
    allowComments = true,
  ) => {
    if (images.length === 0) {
      return request<unknown>(`/v1/channels/${channelId}/notify`, {
        method: 'POST',
        body: { title, body, commentsAllowed: allowComments },
      })
    }
    const form = new FormData()
    form.set('title', title)
    form.set('body', body)
    form.set('commentsAllowed', String(allowComments))
    for (const file of images) form.append('images', file)
    return request<unknown>(`/v1/channels/${channelId}/notify`, { method: 'POST', body: form })
  },

  // Devices & subscriptions
  createDevice: (body: { platform: Platform; fcmToken?: string; webPushSub?: string }) =>
    request<Device>('/v1/devices', { method: 'POST', body }),
  subscribe: (channelId: string, deviceId: string) =>
    request<unknown>(`/v1/channels/${channelId}/subscribe`, { method: 'POST', body: { deviceId } }),
  unsubscribe: (channelId: string, deviceId: string) =>
    request<void>(`/v1/channels/${channelId}/subscribe?deviceId=${encodeURIComponent(deviceId)}`, {
      method: 'DELETE',
    }),
  channelSubscription: (channelId: string, deviceId: string) =>
    request<{ status: 'active' | 'pending' | 'none' }>(
      `/v1/channels/${channelId}/subscription?deviceId=${encodeURIComponent(deviceId)}`,
    ).then((r) => r.status),

  // Conversations (private chats). Content is opaque; encode/decode via lib/chatContent.
  listConversations: () =>
    request<{ conversations: Conversation[] }>('/v1/conversations').then((r) => r.conversations ?? []),
  getConversation: (id: string) => request<Conversation>(`/v1/conversations/${id}`),
  createDirectChat: (otherUserId: string) =>
    request<Conversation>('/v1/conversations', {
      method: 'POST',
      body: { kind: 'direct', memberIds: [otherUserId] },
    }),
  createGroupChat: (title: string, memberIds: string[]) =>
    request<Conversation>('/v1/conversations', {
      method: 'POST',
      body: { kind: 'group', title, memberIds },
    }),
  listChatMessages: (conversationId: string, cursor = '', limit = 50, quiet = false) => {
    const q = new URLSearchParams({ limit: String(limit) })
    if (cursor) q.set('cursor', cursor)
    return request<{ messages: ChatMessage[]; nextCursor: string }>(
      `/v1/conversations/${conversationId}/messages?${q.toString()}`,
      { quiet },
    ).then((r) => ({ messages: r.messages ?? [], nextCursor: r.nextCursor ?? '' }))
  },
  sendChatMessage: (conversationId: string, ciphertext: string, contentType: string) =>
    request<ChatMessage>(`/v1/conversations/${conversationId}/messages`, {
      method: 'POST',
      body: { ciphertext, contentType },
    }),
  /**
   * The conversation's current members, straight from the server.
   *
   * Reconciliation must never decide who belongs in an encrypted group from a Conversation
   * object it happens to be holding: that object was fetched at some point in the past, and
   * a member added since then looks like a stranger — one the group will promptly remove
   * again.
   */
  listConversationMembers: (conversationId: string) =>
    request<{ members: ConversationMember[] }>(
      `/v1/conversations/${conversationId}/members`,
    ).then((r) => r.members ?? []),
  addConversationMember: (conversationId: string, userId: string) =>
    request<ConversationMember>(`/v1/conversations/${conversationId}/members`, {
      method: 'POST',
      body: { userId },
    }),
  removeConversationMember: (conversationId: string, userId: string) =>
    request<void>(`/v1/conversations/${conversationId}/members/${userId}`, { method: 'DELETE' }),
  setConversationMemberRole: (conversationId: string, userId: string, role: 'admin' | 'user') =>
    request<void>(`/v1/conversations/${conversationId}/members/${userId}`, {
      method: 'PATCH',
      body: { role },
    }),
  deleteConversation: (conversationId: string) =>
    request<void>(`/v1/conversations/${conversationId}`, { method: 'DELETE' }),

  // Messages
  getMessage: (channelId: string, messageId: string) =>
    request<Message>(`/v1/channels/${channelId}/messages/${messageId}`),
  listMessages: (channelId: string, cursor = '', query = '', limit = 50, quiet = false) => {
    const q = new URLSearchParams()
    if (cursor) q.set('cursor', cursor)
    if (query) q.set('q', query)
    q.set('limit', String(limit))
    return request<MessagesPage>(`/v1/channels/${channelId}/messages?${q.toString()}`, {
      quiet,
    }).then((page) => ({ messages: page.messages ?? [], nextCursor: page.nextCursor ?? '' }))
  },
  /** A window of messages centred on one — how a search hit is shown in context. */
  messagesAround: (channelId: string, messageId: string, limit = 50) => {
    const q = new URLSearchParams({ around: messageId, limit: String(limit) })
    return request<MessagesPage>(`/v1/channels/${channelId}/messages?${q.toString()}`).then(
      (page) => ({ messages: page.messages ?? [], nextCursor: page.nextCursor ?? '' }),
    )
  },

  deleteMessage: (channelId: string, messageId: string) =>
    request<void>(`/v1/channels/${channelId}/messages/${messageId}`, { method: 'DELETE' }),

  // Comments on a message
  listComments: (channelId: string, messageId: string, cursor = '', limit = 50) => {
    const q = new URLSearchParams()
    if (cursor) q.set('cursor', cursor)
    q.set('limit', String(limit))
    return request<CommentsPage>(
      `/v1/channels/${channelId}/messages/${messageId}/comments?${q.toString()}`,
    ).then((page) => ({ comments: page.comments ?? [], nextCursor: page.nextCursor ?? '' }))
  },
  postComment: (channelId: string, messageId: string, body: string) =>
    request<Comment>(`/v1/channels/${channelId}/messages/${messageId}/comments`, {
      method: 'POST',
      body: { body },
    }),
  deleteComment: (channelId: string, messageId: string, commentId: string) =>
    request<void>(`/v1/channels/${channelId}/messages/${messageId}/comments/${commentId}`, {
      method: 'DELETE',
    }),

  // --- Admin ---
  adminStats: () => request<AdminStats>('/v1/admin/stats'),
  adminListUsers: (q = '', page = 1, limit = 20) => {
    const p = new URLSearchParams({ page: String(page), limit: String(limit) })
    if (q) p.set('q', q)
    return request<{ users: AdminUser[]; total: number; page: number; limit: number }>(
      `/v1/admin/users?${p.toString()}`,
    ).then((r) => ({ items: r.users ?? [], total: r.total, page: r.page, limit: r.limit }))
  },
  adminCreateUser: (body: { email: string; password: string; role: Role }) =>
    request<AdminUser>('/v1/admin/users', { method: 'POST', body }),
  adminUpdateUser: (userId: string, body: { role?: Role; status?: UserStatus }) =>
    request<unknown>(`/v1/admin/users/${userId}`, { method: 'PATCH', body }),
  adminResetUserPassword: (userId: string, newPassword: string) =>
    request<unknown>(`/v1/admin/users/${userId}/reset-password`, { method: 'POST', body: { newPassword } }),
  adminDeleteUser: (userId: string) =>
    request<void>(`/v1/admin/users/${userId}`, { method: 'DELETE' }),
  adminListChannels: (q = '', page = 1, limit = 20) => {
    const p = new URLSearchParams({ page: String(page), limit: String(limit) })
    if (q) p.set('q', q)
    return request<{ channels: AdminChannel[]; total: number; page: number; limit: number }>(
      `/v1/admin/channels?${p.toString()}`,
    ).then((r) => ({ items: r.channels ?? [], total: r.total, page: r.page, limit: r.limit }))
  },
  adminUpdateChannelStatus: (channelId: string, status: ChannelStatus) =>
    request<Channel>(`/v1/admin/channels/${channelId}`, { method: 'PATCH', body: { status } }),
  adminDeleteChannel: (channelId: string) =>
    request<void>(`/v1/admin/channels/${channelId}`, { method: 'DELETE' }),
  adminChannelMessages: (channelId: string, cursor = '', query = '', limit = 50) => {
    const p = new URLSearchParams({ limit: String(limit) })
    if (cursor) p.set('cursor', cursor)
    if (query) p.set('q', query)
    return request<MessagesPage>(`/v1/admin/channels/${channelId}/messages?${p.toString()}`).then(
      (page) => ({ messages: page.messages ?? [], nextCursor: page.nextCursor ?? '' }),
    )
  },
  adminListKeys: (channelId: string) =>
    request<{ keys: ApiKey[] }>(`/v1/admin/channels/${channelId}/keys`).then((r) => r.keys ?? []),
  adminRevokeKey: (channelId: string, keyId: string) =>
    request<unknown>(`/v1/admin/channels/${channelId}/keys/${keyId}`, { method: 'DELETE' }),
  adminListComments: (q = '', page = 1, limit = 20) => {
    const p = new URLSearchParams({ page: String(page), limit: String(limit) })
    if (q) p.set('q', q)
    return request<{ comments: AdminComment[]; total: number; page: number; limit: number }>(
      `/v1/admin/comments?${p.toString()}`,
    ).then((r) => ({ items: r.comments ?? [], total: r.total, page: r.page, limit: r.limit }))
  },
  adminDeleteComment: (commentId: string) =>
    request<void>(`/v1/admin/comments/${commentId}`, { method: 'DELETE' }),
}

/** Absolute URL of a processed message image (served publicly by the App API). */
export function imageUrl(id: string): string {
  return `${BASE}/v1/images/${id}`
}

/** Builds the SSE stream URL with the current access token as a query parameter. */
export function streamUrl(): string | null {
  const tokens = loadTokens()
  if (!tokens) return null
  return `${BASE}/v1/stream?token=${encodeURIComponent(tokens.accessToken)}`
}
