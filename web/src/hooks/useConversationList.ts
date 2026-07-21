import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { deserializeContent } from '../lib/chatContent'
import { getPreview } from '../lib/chatCache'
import { applyReceipt } from '../lib/receipts'
import { MLS_APPLICATION, MLS_CONTROL_TYPES, base64ToBytes } from '../lib/mls'
import { loadLastSeen, markSeen } from '../lib/lastSeen'
import { useAuth } from '../auth/context'
import { useEventStream, useStreamReconnect } from './useEventStream'
import type { ChatMessage, Conversation } from '../lib/types'

/** A conversation as the chat list renders it, next to channel rows. */
export interface ChatConversation {
  id: string
  conversation: Conversation
  /** Decoded preview of the newest message; empty when none or undecodable. */
  preview: string
  lastActivity: string
  unread: boolean
}

export interface ConversationListApi {
  conversations: ChatConversation[]
  loading: boolean
  refresh: () => Promise<void>
  markRead: (conversationId: string, iso: string, seq?: number) => void
}

// The list cannot decrypt: MLS deletes each message key after a single use, and
// only the open conversation view decrypts (and caches) a message. So the preview
// comes from that local cache, written when the chat was last open. A conversation
// with an unread message you have not opened shows a generic placeholder until you
// do — the plaintext genuinely is not available anywhere yet.
function previewOf(conv: Conversation, encryptedLabel: string): string {
  const cached = getPreview(conv.id)
  if (cached) return cached
  const msg = conv.lastMessage
  if (!msg) return ''
  if (msg.contentType === MLS_APPLICATION) return encryptedLabel
  // Protocol traffic — a Welcome, a Commit, a device announcing itself. None of it is
  // something a human said, so none of it belongs in the list.
  if (MLS_CONTROL_TYPES.has(msg.contentType)) return ''
  // Legacy plaintext content (pre-encryption) still decodes directly.
  return deserializeContent(base64ToBytes(msg.ciphertext))?.body ?? ''
}

/** Merges a live chat message into its conversation's lastMessage. */
function patchLast(conv: Conversation, msg: ChatMessage): Conversation {
  return {
    ...conv,
    lastMessage: {
      id: msg.id,
      senderId: msg.senderId,
      ciphertext: msg.ciphertext,
      contentType: msg.contentType,
      createdAt: msg.createdAt,
    },
  }
}

/**
 * The private-conversation half of the chat list — the counterpart of
 * useChannelList. Kept a separate hook so the two data sources stay independent;
 * the sidebar merges their outputs.
 */
export function useConversationList(): ConversationListApi {
  const { userId } = useAuth()
  const { t } = useTranslation()
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [loading, setLoading] = useState(true)
  const [seen, setSeen] = useState<Readonly<Record<string, string>>>(() => loadLastSeen())

  const refresh = useCallback(async () => {
    setConversations(await api.listConversations())
  }, [])

  // The ids currently in the list, for the live handler to test membership without
  // a stale closure over `conversations`. A message for an id NOT here is the first
  // sign of a conversation created for us elsewhere — a cross-host mirror, or being
  // added to a group — and is what triggers a refresh to pull it in.
  const idsRef = useRef<Set<string>>(new Set())
  useEffect(() => {
    idsRef.current = new Set(conversations.map((c) => c.id))
  }, [conversations])

  useEffect(() => {
    let active = true
    api
      .listConversations()
      .then((cs) => active && setConversations(cs))
      .catch(() => undefined)
      .finally(() => active && setLoading(false))
    return () => {
      active = false
    }
  }, [])

  // A live message updates its conversation's preview and re-sorts it up. A
  // message for a conversation not yet in the list (just created elsewhere) is
  // ignored; refresh() picks it up.
  useEventStream((e) => {
    if (e.conversationId && e.conversationDeleted) {
      setConversations((prev) => prev.filter((c) => c.id !== e.conversationId))
      return
    }
    if (!e.conversationId || !e.chatMessage) return
    const msg = e.chatMessage
    // A message for a conversation we do not have yet — one just created for us on
    // another host (a mirror), or one we were just added to. Pull the list so it
    // appears now instead of on the next reload.
    if (!idsRef.current.has(e.conversationId)) {
      void refresh()
      return
    }
    // MLS protocol traffic (a device announcing itself, a Welcome, a Commit) is not
    // something a human said. Opening a chat generates it — announceDevice re-fires on
    // every open until this device is admitted — so letting it patch lastMessage would
    // bump the row to the top of the list with a blank preview just for being opened.
    // Only a real message reorders a conversation. previewOf already excludes these.
    if (MLS_CONTROL_TYPES.has(msg.contentType)) return
    setConversations((prev) =>
      prev.map((c) => (c.id === e.conversationId ? patchLast(c, msg) : c)),
    )

    // It has reached this device — which is what one tick means. Reported from HERE, the app-wide
    // stream, rather than from the open chat: delivery is not about looking at the conversation, and
    // most messages arrive while its window is somewhere else entirely. Never for our own echo:
    // a sender does not deliver to themselves, and the tick already ignores their own watermark.
    const conversationId = e.conversationId
    if (msg.senderId === userId) return
    // Messages predating sequencing carry seq 0/undefined; there is no watermark to move for them,
    // and the server rejects a report of 0.
    const seq = msg.seq ?? 0
    if (seq === 0) return
    if ((reportedDelivered.current[conversationId] ?? 0) >= seq) return
    reportedDelivered.current[conversationId] = seq
    void api.reportReceipt(conversationId, { deliveredSeq: seq }).catch(() => {
      // See markRead: a lost receipt costs a tick, never a message.
    })
  })

  // A receipt from someone else: patch the member it belongs to, so an open chat's ticks move.
  useEventStream((e) => {
    if (!e.conversationId || !e.receipt) return
    const receipt = e.receipt
    setConversations((prev) =>
      prev.map((c) =>
        c.id === e.conversationId ? { ...c, members: applyReceipt(c.members, receipt) } : c,
      ),
    )
  })

  // A dropped stream can miss a one-shot deletion, leaving a ghost row that only a
  // reload clears. Re-fetching on reconnect reconciles it: a conversation gone on the
  // server drops from the list without the user having to reload.
  useStreamReconnect(refresh)

  // The furthest point already reported to the server, per conversation, so a receipt goes once per
  // advance rather than on every scroll to the bottom. Refs, not state: nothing renders from them,
  // and they must be readable inside callbacks without going stale.
  const reportedRead = useRef<Record<string, number>>({})
  const reportedDelivered = useRef<Record<string, number>>({})

  const markRead = useCallback((conversationId: string, iso: string, seq?: number) => {
    markSeen(conversationId, iso)
    setSeen((prev) => ((prev[conversationId] ?? '') >= iso ? prev : { ...prev, [conversationId]: iso }))

    // Tell the sender their newest read message has been read. Only ever forwards, and only when it
    // actually moves — this is called every time the feed touches the bottom. Messages predating
    // sequencing carry seq 0/undefined, which has no watermark to move and the server rejects.
    const readSeq = seq ?? 0
    if (readSeq === 0) return
    if ((reportedRead.current[conversationId] ?? 0) >= readSeq) return
    reportedRead.current[conversationId] = readSeq
    void api.reportReceipt(conversationId, { readSeq }).catch(() => {
      // A receipt is a courtesy: a lost one costs a tick until the next advance, never a message.
      // Left un-rolled-back on purpose — retrying every failure would hammer a server that is
      // already struggling.
    })
  }, [])

  const encryptedLabel = t('chat.encryptedPreview')
  const ordered = useMemo<ChatConversation[]>(() => {
    return conversations
      .map((c) => {
        const lastActivity = c.lastMessage?.createdAt ?? c.createdAt
        // Unread when the newest message is newer than last seen AND not our own —
        // our own sends never count as unread.
        //
        // Protocol traffic is not something to read, so it can never make a chat unread. The
        // server already keeps it out of lastMessage; this mirrors the guard the mobile app has
        // always had (unreadProvider), because getting it wrong lights a dot that CANNOT be
        // cleared: opening the chat marks read up to the newest real message, which is older
        // than the announcement that lit it.
        const last = c.lastMessage
        const fromOther = last ? last.senderId !== userId : false
        const readable = last ? !MLS_CONTROL_TYPES.has(last.contentType) : false
        const unread = fromOther && readable && lastActivity > (seen[c.id] ?? '')
        return {
          id: c.id,
          conversation: c,
          preview: previewOf(c, encryptedLabel),
          lastActivity,
          unread,
        }
      })
      .sort((a, b) => b.lastActivity.localeCompare(a.lastActivity))
  }, [conversations, seen, userId, encryptedLabel])

  return { conversations: ordered, loading, refresh, markRead }
}
