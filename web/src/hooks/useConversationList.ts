import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { deserializeContent } from '../lib/chatContent'
import { getPreview } from '../lib/chatCache'
import { MLS_APPLICATION, MLS_WELCOME, base64ToBytes } from '../lib/mls'
import { loadLastSeen, markSeen } from '../lib/lastSeen'
import { useAuth } from '../auth/context'
import { useEventStream } from './useEventStream'
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
  markRead: (conversationId: string, iso: string) => void
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
  if (msg.contentType === MLS_WELCOME) return ''
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
    setConversations((prev) =>
      prev.map((c) => (c.id === e.conversationId ? patchLast(c, msg) : c)),
    )
  })

  const markRead = useCallback((conversationId: string, iso: string) => {
    markSeen(conversationId, iso)
    setSeen((prev) => ((prev[conversationId] ?? '') >= iso ? prev : { ...prev, [conversationId]: iso }))
  }, [])

  const encryptedLabel = t('chat.encryptedPreview')
  const ordered = useMemo<ChatConversation[]>(() => {
    return conversations
      .map((c) => {
        const lastActivity = c.lastMessage?.createdAt ?? c.createdAt
        // Unread when the newest message is newer than last seen AND not our own —
        // our own sends never count as unread.
        const fromOther = c.lastMessage ? c.lastMessage.senderId !== userId : false
        const unread = fromOther && lastActivity > (seen[c.id] ?? '')
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
