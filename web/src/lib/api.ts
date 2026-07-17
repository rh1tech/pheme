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
  MLSDevice,
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

/**
 * Sends and receives raw bytes, with the same bearer token and the same one-shot refresh as
 * `request` — but no JSON on either side.
 *
 * Attachments cannot go through `request`. What travels here is a sealed photo: the body is
 * ciphertext, and the response is ciphertext, and wrapping either in an envelope would mean
 * base64-ing it for no reason and handing the server a content type it has no business knowing.
 */
async function requestBinary(
  path: string,
  init: { method: 'GET' | 'POST'; body?: Uint8Array },
): Promise<Response> {
  const tokens = loadTokens()
  if (!tokens) {
    onAuthFailure?.()
    throw new AuthError('not authenticated')
  }

  const send = (accessToken: string): Promise<Response> =>
    fetch(`${BASE}${path}`, {
      method: init.method,
      headers: {
        Authorization: `Bearer ${accessToken}`,
        ...(init.body ? { 'Content-Type': 'application/octet-stream' } : {}),
      },
      body: init.body ? (init.body as BodyInit) : undefined,
    })

  let res = await send(tokens.accessToken)
  if (res.status === 401) {
    res = await send(await refresh(tokens))
  }
  if (!res.ok) {
    throw new ApiError(res.status, `attachment request failed (${res.status})`)
  }
  return res
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
  publishKeyPackages: (
    deviceId: string,
    keyPackages: string[],
    lastResortKeyPackage?: string,
    label?: string,
  ) =>
    request<void>('/v1/mls/key-packages', {
      method: 'POST',
      body: { deviceId, keyPackages, lastResortKeyPackage, label },
    }),

  /** The signed-in user's own devices — for the "your devices" panel. */
  myDevices: () =>
    request<{ devices: MLSDevice[] }>('/v1/mls/devices').then((r) => r.devices ?? []),
  /**
   * Registers this device in the user's own registry and refreshes its last-seen, without
   * publishing key packages — called on session load so a device lists itself from launch.
   */
  registerMlsDevice: (deviceId: string, label: string) =>
    request<void>('/v1/mls/devices', {
      method: 'POST',
      body: { deviceId, label },
    }),
  /**
   * Terminates one of the caller's own devices server-side: deletes its published key
   * packages so it cannot be re-added, revokes its login, and forgets it from the registry.
   * The MLS leaf removal is done first, client-side, by terminateOwnDevice in lib/mls.
   */
  terminateDevice: (deviceId: string) =>
    request<void>(`/v1/mls/devices/${encodeURIComponent(deviceId)}`, { method: 'DELETE' }),
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

  /**
   * The STUN/TURN servers this browser should use to find its peer, with a short-lived
   * TURN credential. 503 when calling is not configured on the server.
   *
   * Fetched per call, not cached: the credential expires, and a stale one fails a relayed
   * call in the most confusing way possible (the browser just reports that no candidate
   * pair worked).
   */
  iceServers: () => request<{ iceServers: RTCIceServer[] }>('/v1/calls/ice-servers'),

  /**
   * Relays one sealed signal. `ring` wakes the other person's devices with a push, and only
   * the invite sets it — the rest of the exchange rides the live stream, and pushing for
   * every signal would buzz a phone half a dozen times per call.
   */
  callSignal: (
    conversationId: string,
    callId: string,
    ciphertext: string,
    opts: { ring?: boolean; cancel?: boolean } = {},
  ) =>
    request<{ seq: number }>(`/v1/conversations/${conversationId}/calls/${callId}/signal`, {
      method: 'POST',
      body: { ciphertext, ring: opts.ring ?? false, cancel: opts.cancel ?? false },
      quiet: true,
    }),

  /**
   * Everything this device has not seen yet, in order.
   *
   * This is the transport of record, not the live stream: the live bus is allowed to drop
   * events, and a dropped SDP answer is a call that silently never connects. The stream only
   * nudges; the signals are fetched from here.
   */
  callSignals: (conversationId: string, callId: string, since: number) =>
    request<{ signals: { seq: number; ciphertext: string }[] }>(
      `/v1/conversations/${conversationId}/calls/${callId}/signals?since=${since}`,
      { quiet: true },
    ).then((r) => r.signals ?? []),

  /**
   * Re-nudges the other end while the call is still ringing.
   *
   * The invite goes out once, and a callee whose live stream is momentarily down — mid
   * reconnect, backgrounded, changing cells — simply misses it. The invite is still in the
   * mailbox; nothing was looking. This asks the server to point at it again. No push: the
   * phone was already buzzed by the invite.
   */
  callRing: (conversationId: string, callId: string) =>
    request<void>(`/v1/conversations/${conversationId}/calls/${callId}/ring`, {
      method: 'POST',
      quiet: true,
    }),

  /**
   * Claims the call for THIS device. Resolves true if we answered it, false if another of
   * our devices got there first — an answer, not an error, and the reason it is decided by
   * the server rather than by a race over a bus that may drop the message.
   */
  callAccept: (conversationId: string, callId: string, deviceId: string) =>
    request<{ winner: string }>(`/v1/conversations/${conversationId}/calls/${callId}/accept`, {
      method: 'POST',
      body: { deviceId },
      quiet: true,
    })
      .then(() => true)
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 409) return false
        throw e
      }),

  /** The conversation's MLS group id and epoch. `groupId` is empty until it is established. */
  mlsGroupState: (conversationId: string) =>
    request<MLSGroupState>(`/v1/conversations/${conversationId}/mls`),

  /**
   * The latest GroupInfo a member has published for this conversation's current group — the
   * self-contained snapshot a NEW device needs to join by external commit, with no member online
   * to admit it. Null (404) is a real answer: no member has published one yet, so the joiner has
   * to fall back to announce-and-wait.
   */
  mlsGroupInfo: (conversationId: string) =>
    request<{ groupId: string; epoch: number; groupInfo: string } | null>(
      `/v1/conversations/${conversationId}/mls/group-info`,
      { allow404: true },
    ),

  /**
   * Publishes fresh GroupInfo (base64) at `epoch`, so the NEXT new device can external-join at the
   * current epoch instead of waiting to be admitted. Fire-and-forget after any accepted commit.
   */
  publishGroupInfo: (
    conversationId: string,
    body: { groupId: string; epoch: number; groupInfo: string },
  ) =>
    request<void>(`/v1/conversations/${conversationId}/mls/group-info`, {
      method: 'POST',
      body,
    }),

  /**
   * Uploads a sealed transcript blob for a joining device to fetch, returning its id. Opaque bytes —
   * sealed under a group-derived key the server cannot derive.
   */
  uploadHistory: async (conversationId: string, sealed: Uint8Array): Promise<string> => {
    const res = await requestBinary(`/v1/conversations/${conversationId}/mls/history`, {
      method: 'POST',
      body: sealed,
    })
    const out = (await res.json()) as { id?: string }
    return out.id ?? ''
  },

  /** Fetches a sealed transcript blob once (the server deletes it after). Still ciphertext. */
  getHistory: async (conversationId: string, historyId: string): Promise<Uint8Array> => {
    const res = await requestBinary(
      `/v1/conversations/${conversationId}/mls/history/${historyId}`,
      { method: 'GET' },
    )
    return new Uint8Array(await res.arrayBuffer())
  },

  /**
   * Retires a group nobody can use any more, so the conversation can start a fresh one.
   *
   * The last resort, and only called by a device that has announced itself and given up on
   * being let in. It destroys nothing — the retired group is remembered, and anyone who still
   * holds it can still read every message that was sent to it.
   */
  mlsResetGroup: (conversationId: string) =>
    request<MLSGroupState>(`/v1/conversations/${conversationId}/mls/reset`, { method: 'POST' }),

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
  // sees the passphrase or the plaintext state. The transcript blob rides along
  // optionally — the decrypted message cache, sealed under the same passphrase, so a
  // restore recovers the words and not just the keys.
  putKeyBackup: (
    deviceId: string,
    salt: string,
    nonce: string,
    ciphertext: string,
    transcript?: { salt: string; nonce: string; ciphertext: string },
  ) =>
    request<void>('/v1/mls/key-backup', {
      method: 'PUT',
      body: {
        deviceId,
        salt,
        nonce,
        ciphertext,
        ...(transcript
          ? {
              transcriptSalt: transcript.salt,
              transcriptNonce: transcript.nonce,
              transcriptCiphertext: transcript.ciphertext,
            }
          : {}),
      },
    }),
  getKeyBackup: (quiet = false) =>
    request<{
      salt: string
      nonce: string
      ciphertext: string
      transcriptSalt?: string | null
      transcriptNonce?: string | null
      transcriptCiphertext?: string | null
      updatedAt: string
    } | null>('/v1/mls/key-backup', { quiet, allow404: true }),

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
  /**
   * Like getConversation, but a gone conversation resolves to null instead of
   * throwing — for the on-open existence probe. The server answers 404 both when a
   * conversation was deleted and when this device is no longer a member (it never
   * leaks which), and either way the answer is the same: it is not ours to show.
   */
  getConversationMaybe: (id: string) =>
    request<Conversation | null>(`/v1/conversations/${id}`, { allow404: true }),
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
  /**
   * Reports how far this user has got in a conversation, so the sender's ticks can fill in.
   *
   * Watermarks, not message ids — "I have read up to this instant". Both only ever move forward
   * server-side, so a duplicate or out-of-order report is harmless, and `quiet` keeps it off the
   * progress bar: nobody asked for it.
   */
  reportReceipt: (conversationId: string, at: { delivered?: string; read?: string }) =>
    request<void>(`/v1/conversations/${conversationId}/receipts`, {
      method: 'POST',
      body: at,
      quiet: true,
    }),
  sendChatMessage: (conversationId: string, ciphertext: string, contentType: string) =>
    request<ChatMessage>(`/v1/conversations/${conversationId}/messages`, {
      method: 'POST',
      body: { ciphertext, contentType },
    }),

  /**
   * Uploads one encrypted photo and returns its blob id.
   *
   * The body is raw ciphertext. The server stores it as opaque bytes and is told nothing else — the
   * key that opens it travels inside the MLS-encrypted message that references this id, and never
   * comes here at all.
   */
  uploadAttachment: async (conversationId: string, sealed: Uint8Array): Promise<string> => {
    const res = await requestBinary(`/v1/conversations/${conversationId}/attachments`, {
      method: 'POST',
      body: sealed,
    })
    const out = (await res.json()) as { id?: string }
    return out.id ?? ''
  },

  /** Fetches one encrypted photo. Still ciphertext — the caller opens it with the key from the message. */
  attachmentBytes: async (conversationId: string, attachmentId: string): Promise<Uint8Array> => {
    const res = await requestBinary(
      `/v1/conversations/${conversationId}/attachments/${attachmentId}`,
      { method: 'GET' },
    )
    return new Uint8Array(await res.arrayBuffer())
  },
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
  /**
   * Purges every stored message of a conversation server-side while keeping the
   * conversation itself. The ciphertext is opaque and, with MLS forward secrecy,
   * unreadable to the server anyway — this frees the storage and stops the history
   * re-syncing to a fresh device. The caller clears the local plaintext caches too.
   */
  clearChatHistory: (conversationId: string) =>
    request<void>(`/v1/conversations/${conversationId}/messages`, { method: 'DELETE' }),

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

// A stream connection must last longer than the token that opened it is valid for,
// and the token cannot be renewed in flight — EventSource cannot set headers, so the
// token is baked into the URL at connect time. Refresh anything that would expire
// mid-connection before opening, or the server would hang up on us almost at once.
const STREAM_TOKEN_FLOOR_MS = 2 * 60 * 1000

/** Seconds-since-epoch expiry of a JWT, or null if it has no readable `exp`. */
function tokenExpiry(jwt: string): number | null {
  const payload = jwt.split('.')[1]
  if (!payload) return null
  try {
    const claims = JSON.parse(atob(payload.replace(/-/g, '+').replace(/_/g, '/')))
    return typeof claims.exp === 'number' ? claims.exp : null
  } catch {
    return null // not a JWT we can read; treat as opaque and let the server judge it
  }
}

/**
 * Builds the SSE stream URL, refreshing the access token first if it is expired or
 * close enough to expiry that the connection would not outlive it.
 *
 * Returns null when there is no usable session.
 */
export async function streamUrl(): Promise<string | null> {
  const tokens = loadTokens()
  if (!tokens) return null

  let access = tokens.accessToken
  const exp = tokenExpiry(access)
  if (exp === null || exp * 1000 - Date.now() < STREAM_TOKEN_FLOOR_MS) {
    try {
      access = await refresh(tokens)
    } catch {
      return null // refresh() has already cleared the session and signalled auth failure
    }
  }
  return `${BASE}/v1/stream?token=${encodeURIComponent(access)}`
}
