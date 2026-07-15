import { useCallback, useMemo, useRef, useState } from 'react'
import { Group, Loader, Stack, Text, TextInput } from '@mantine/core'
import { IconSearch } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { usePullToRefresh } from '../../hooks/usePullToRefresh'
import { ChatSidebarMenu } from './ChatSidebarMenu'
import { NewChannelMenu } from './NewChannelMenu'
import { ChatListItem } from './ChatListItem'
import { ConversationListItem } from './ConversationListItem'
import { NotificationsBanner } from '../NotificationsBanner'
import { CardListSkeleton } from '../Skeletons'
import { conversationTitle } from '../../lib/conversation'
import { useAuth } from '../../auth/context'
import type { ChannelListApi } from '../../hooks/useChannelList'
import type { ConversationListApi } from '../../hooks/useConversationList'

interface ChatSidebarProps {
  list: ChannelListApi
  conversations: ConversationListApi
  /** The channel or conversation currently open, if any. */
  activeId?: string
  /** Fired on every pick, including a re-pick of the row already open. */
  onSelectChannel: () => void
}

// One merged row model so channels and conversations sort together by activity,
// the way Telegram draws a single list.
type Row =
  | { kind: 'channel'; id: string; activity: string; text: string }
  | { kind: 'conversation'; id: string; activity: string; text: string }

export function ChatSidebar({ list, conversations, activeId, onSelectChannel }: ChatSidebarProps) {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')

  // Channels and conversations become one list, ordered by last activity. The
  // filter matches a channel's name/alias or a conversation's title, all in memory.
  const rows = useMemo<Row[]>(() => {
    const channelRows: Row[] = list.channels.map((c) => ({
      kind: 'channel',
      id: c.id,
      activity: c.lastMessage?.createdAt ?? c.createdAt,
      text: `${c.name} ${c.alias ?? ''}`,
    }))
    const convRows: Row[] = conversations.conversations.map((c) => ({
      kind: 'conversation',
      id: c.id,
      activity: c.lastActivity,
      text: conversationTitle(c.conversation, userId ?? ''),
    }))
    const merged = [...channelRows, ...convRows]
    const q = query.trim().toLowerCase()
    const filtered = q ? merged.filter((r) => r.text.toLowerCase().includes(q)) : merged
    // Channels first, then chats; within each, most recent activity on top. The two
    // are different kinds of thing — a broadcast you follow versus a conversation you
    // are in — so they read better grouped than interleaved by time.
    return filtered.sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === 'channel' ? -1 : 1
      return b.activity.localeCompare(a.activity)
    })
  }, [list.channels, conversations.conversations, query, userId])

  const loading = list.loading || conversations.loading
  const total = list.channels.length + conversations.conversations.length
  const empty = !loading && total === 0
  const noMatches = !loading && total > 0 && rows.length === 0

  const channelById = useMemo(() => new Map(list.channels.map((c) => [c.id, c])), [list.channels])
  const convById = useMemo(
    () => new Map(conversations.conversations.map((c) => [c.id, c])),
    [conversations.conversations],
  )

  function select(kind: Row['kind'], id: string) {
    onSelectChannel()
    navigate(kind === 'channel' ? `/channels/${id}` : `/chats/${id}`)
  }

  // Pull down from the top of the list to re-fetch both halves — the same refresh the
  // stream runs on reconnect, now on demand. A phone gesture, so it is off on desktop
  // where the pointer cannot express it.
  const listRef = useRef<HTMLDivElement>(null)
  const refreshAll = useCallback(
    () => Promise.all([list.refresh(), conversations.refresh()]).then(() => undefined),
    [list, conversations],
  )
  const { pull, refreshing } = usePullToRefresh(listRef, refreshAll)

  return (
    <aside className="pheme-sidebar" data-testid="chat-sidebar">
      <div className="pheme-sidebar-header">
        <Group gap="xs" wrap="nowrap">
          <ChatSidebarMenu />
          <TextInput
            aria-label={t('chat.searchChannels')}
            placeholder={t('chat.searchChannels')}
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            leftSection={<IconSearch size={16} stroke={1.8} />}
            radius="xl"
            style={{ flex: 1 }}
          />
          <NewChannelMenu
            onChanged={async () => {
              await Promise.all([list.refresh(), conversations.refresh()])
            }}
          />
        </Group>
      </div>

      <div className="pheme-sidebar-list" ref={listRef}>
        {(pull > 0 || refreshing) && (
          <div
            className="pheme-ptr"
            style={{ transform: `translateY(${pull}px)` }}
            aria-hidden={!refreshing}
          >
            <Loader size="sm" />
          </div>
        )}
        <NotificationsBanner />

        {loading && <CardListSkeleton rows={5} />}

        {(empty || noMatches) && (
          <Stack align="center" py="xl">
            <Text c="dimmed" size="sm">
              {empty ? t('chat.noChannels') : t('chat.noResults')}
            </Text>
          </Stack>
        )}

        {rows.map((row) => {
          if (row.kind === 'channel') {
            const channel = channelById.get(row.id)
            if (!channel) return null
            return (
              <ChatListItem
                key={`ch-${row.id}`}
                channel={channel}
                active={row.id === activeId}
                onSelect={(id) => select('channel', id)}
              />
            )
          }
          const item = convById.get(row.id)
          if (!item) return null
          return (
            <ConversationListItem
              key={`co-${row.id}`}
              item={item}
              active={row.id === activeId}
              onSelect={(id) => select('conversation', id)}
            />
          )
        })}
      </div>
    </aside>
  )
}
