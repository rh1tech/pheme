import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import { loadLastSeen, markSeen } from '../lib/lastSeen'
import { useEventStream, useStreamReconnect } from './useEventStream'
import type {
  Channel,
  ChannelRole,
  JoinedChannel,
  LastMessage,
  MemberStatus,
  Message,
  SubscriptionMode,
} from '../lib/types'

/** A channel as the chat list renders it: owned and joined channels, merged. */
export interface ChatChannel {
  id: string
  name: string
  alias?: string
  avatarId?: string
  subscriptionMode: SubscriptionMode
  isOwner: boolean
  role: ChannelRole
  memberStatus: MemberStatus
  /** Ordering key and preview source. Absent until the channel has a message. */
  lastMessage?: LastMessage
  /** Newer than what this browser last displayed. */
  unread: boolean
  createdAt: string
}

export interface ChannelListApi {
  channels: ChatChannel[]
  loading: boolean
  refresh: () => Promise<void>
  /** Records the channel as read up to the given message timestamp. */
  markRead: (channelId: string, iso: string) => void
}

/** Ordering key: newest activity first, falling back to when it was created. */
function sortKey(c: ChatChannel): string {
  return c.lastMessage?.createdAt ?? c.createdAt
}

function toLastMessage(m: Message): LastMessage {
  return {
    id: m.id,
    title: m.title,
    body: m.body,
    imageCount: m.images?.length ?? 0,
    createdAt: m.createdAt,
  }
}

function merge(owned: Channel[], joined: JoinedChannel[]): ChatChannel[] {
  const fromOwned: ChatChannel[] = owned.map((c) => ({
    id: c.id,
    name: c.name,
    alias: c.alias,
    avatarId: c.avatarId,
    subscriptionMode: c.subscriptionMode,
    isOwner: true,
    role: 'admin',
    memberStatus: 'active',
    lastMessage: c.lastMessage,
    unread: false,
    createdAt: c.createdAt,
  }))
  const fromJoined: ChatChannel[] = joined.map((c) => ({
    id: c.id,
    name: c.name,
    alias: c.alias,
    avatarId: c.avatarId,
    subscriptionMode: c.subscriptionMode,
    isOwner: false,
    role: c.role,
    memberStatus: c.memberStatus,
    lastMessage: c.lastMessage,
    unread: false,
    createdAt: c.createdAt,
  }))
  return [...fromOwned, ...fromJoined]
}

/**
 * The chat list's data: owned and joined channels as one list — Telegram draws no
 * line between a channel you run and one you follow — ordered by latest activity
 * and kept live by the message stream.
 */
export function useChannelList(): ChannelListApi {
  const [channels, setChannels] = useState<ChatChannel[]>([])
  const [loading, setLoading] = useState(true)
  const [seen, setSeen] = useState<Readonly<Record<string, string>>>(() => loadLastSeen())

  const load = useCallback(async () => {
    const [owned, joined] = await Promise.all([api.listChannels(), api.listJoinedChannels()])
    setChannels(merge(owned, joined))
  }, [])

  const refresh = useCallback(async () => {
    await load()
  }, [load])

  useEffect(() => {
    let active = true
    const run = async () => {
      try {
        const [owned, joined] = await Promise.all([api.listChannels(), api.listJoinedChannels()])
        if (active) setChannels(merge(owned, joined))
      } catch {
        // A chat list that fails to load shows its empty state; the error is not
        // actionable and a toast on every cold start would be noise.
      } finally {
        if (active) setLoading(false)
      }
    }
    void run()
    return () => {
      active = false
    }
  }, [])

  // A live message updates its channel's preview in place, which re-sorts it to
  // the top of the list. A message for a channel not in the list (just joined in
  // another tab, say) is ignored — refresh() will pick the channel up.
  useEventStream((e) => {
    if (!e.channelId || !e.message) return // ignore conversation events here
    const msg = e.message
    setChannels((prev) =>
      prev.map((c) => (c.id === e.channelId ? { ...c, lastMessage: toLastMessage(msg) } : c)),
    )
  })

  // Reconcile after a stream gap: refetch on reconnect so a channel left or removed
  // elsewhere drops from the list without a reload.
  useStreamReconnect(refresh)

  const markRead = useCallback((channelId: string, iso: string) => {
    markSeen(channelId, iso)
    setSeen((prev) => ((prev[channelId] ?? '') >= iso ? prev : { ...prev, [channelId]: iso }))
  }, [])

  // Unread is derived, never stored: a channel is unread when its newest message
  // is newer than the newest one this browser has displayed.
  const ordered = useMemo(() => {
    return channels
      .map((c) => ({
        ...c,
        unread: c.lastMessage ? c.lastMessage.createdAt > (seen[c.id] ?? '') : false,
      }))
      .sort((a, b) => sortKey(b).localeCompare(sortKey(a)))
  }, [channels, seen])

  return { channels: ordered, loading, refresh, markRead }
}
