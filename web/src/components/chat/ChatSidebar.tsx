import { useCallback, useMemo, useRef, useState } from 'react'
import { Group, Loader, Stack, Text, TextInput } from '@mantine/core'
import { IconMessages, IconSearch, IconSpeakerphone } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { usePullToRefresh } from '../../hooks/usePullToRefresh'
import { useUserSearch } from '../../hooks/useUserSearch'
import { ChatSidebarMenu } from './ChatSidebarMenu'
import { NewChannelMenu } from './NewChannelMenu'
import { ChatListItem } from './ChatListItem'
import { ConversationListItem } from './ConversationListItem'
import { ChannelAvatar } from './ChannelAvatar'
import { NotificationsBanner } from '../NotificationsBanner'
import { CardListSkeleton } from '../Skeletons'
import { api } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { conversationTitle, userLabel } from '../../lib/conversation'
import { useAuth } from '../../auth/context'
import type { PublicUser } from '../../lib/types'
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

type Row =
  | { kind: 'channel'; id: string; activity: string; text: string }
  | { kind: 'conversation'; id: string; activity: string; text: string }

// The two peer sections. Chats first, matching the mobile app's tab order (home_shell.dart).
type Tab = 'chats' | 'channels'

export function ChatSidebar({ list, conversations, activeId, onSelectChannel }: ChatSidebarProps) {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  // Chats and channels are separate lists behind two bottom tabs — the mobile app's information
  // architecture (home_shell.dart), now on the web too. Independent local state, like the app: the
  // open conversation does not drive it.
  const [tab, setTab] = useState<Tab>('chats')

  // Just the active tab's rows, ordered by last activity, filtered by the query in memory.
  const rows = useMemo<Row[]>(() => {
    const source: Row[] =
      tab === 'channels'
        ? list.channels.map((c) => ({
            kind: 'channel',
            id: c.id,
            activity: c.lastMessage?.createdAt ?? c.createdAt,
            text: `${c.name} ${c.alias ?? ''}`,
          }))
        : conversations.conversations.map((c) => ({
            kind: 'conversation',
            id: c.id,
            activity: c.lastActivity,
            text: conversationTitle(c.conversation, userId ?? ''),
          }))
    const q = query.trim().toLowerCase()
    const filtered = q ? source.filter((r) => r.text.toLowerCase().includes(q)) : source
    return filtered.slice().sort((a, b) => b.activity.localeCompare(a.activity))
  }, [tab, list.channels, conversations.conversations, query, userId])

  const loading = tab === 'channels' ? list.loading : conversations.loading
  const total = tab === 'channels' ? list.channels.length : conversations.conversations.length
  const empty = !loading && total === 0

  // A dot on the other tab when it has something unread, so switching away does not hide activity.
  const chatsUnread = conversations.conversations.some((c) => c.unread)
  const channelsUnread = list.channels.some((c) => c.unread)

  const channelById = useMemo(() => new Map(list.channels.map((c) => [c.id, c])), [list.channels])
  const convById = useMemo(
    () => new Map(conversations.conversations.map((c) => [c.id, c])),
    [conversations.conversations],
  )

  function select(kind: Row['kind'], id: string) {
    onSelectChannel()
    navigate(kind === 'channel' ? `/channels/${id}` : `/chats/${id}`)
  }

  // A query turns the active tab's list into a search. On the Chats tab it also finds PEOPLE to start
  // a new chat with (createDirectChat is idempotent — picking someone you already talk to just opens
  // that chat); the Channels tab searches channels only. The results fill the whole list area.
  const searching = query.trim().length > 0
  const { results: people, searching: peopleSearching, active: peopleActive } = useUserSearch(
    tab === 'chats' ? query : '',
  )

  async function startChatWith(user: PublicUser) {
    try {
      const conv = await api.createDirectChat(user.id)
      await conversations.refresh()
      setQuery('')
      onSelectChannel()
      navigate(`/chats/${conv.id}`)
    } catch (e) {
      notifyError(t('chat.startFailed'), e)
    }
  }

  function renderRow(row: Row) {
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
  }

  // The active tab found nothing for the query. On the Chats tab that only counts once people were
  // searched too (a chat OR a person could still match); on the Channels tab there is nothing else
  // to wait for. "Keep typing" is the separate, earlier state where the query is too short to have
  // searched people yet — Chats tab only.
  const noRows = searching && rows.length === 0
  const showKeepTyping = noRows && tab === 'chats' && !peopleActive
  const showNoResults =
    noRows && (tab === 'channels' || (peopleActive && !peopleSearching && people.length === 0))

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
            // Declare it a search field and turn off autofill/autocorrect: without
            // these, iOS floats its native AutoFill/autocomplete dropdown over the
            // field — clipped above the keyboard — the moment it is focused.
            type="search"
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            enterKeyHint="search"
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
        {!searching && <NotificationsBanner />}

        {loading && <CardListSkeleton rows={5} />}

        {/* Not searching: the active tab's list, with its own empty state. */}
        {!searching && empty && (
          <Stack align="center" py="xl">
            <Text c="dimmed" size="sm">
              {tab === 'channels' ? t('chat.noChannels') : t('chat.noChats')}
            </Text>
          </Stack>
        )}
        {!searching && rows.map(renderRow)}

        {/* Searching: the tab's matching rows; on the Chats tab, people to start a chat with too. */}
        {searching && rows.map(renderRow)}

        {searching && peopleActive && (
          <div className="pheme-list-section">{t('chat.peopleSection')}</div>
        )}
        {searching && peopleActive && peopleSearching && people.length === 0 && (
          <Group justify="center" py="sm">
            <Loader size="sm" />
          </Group>
        )}
        {searching &&
          people.map((u) => (
            <button
              key={`pe-${u.id}`}
              type="button"
              className="pheme-chat-row"
              onClick={() => void startChatWith(u)}
            >
              <ChannelAvatar id={u.id} name={userLabel(u)} avatarId={u.avatarId} size={44} />
              <div style={{ minWidth: 0 }}>
                <Text fw={600} size="sm" truncate>
                  {userLabel(u)}
                </Text>
                {u.username && (
                  <Text size="xs" c="dimmed" truncate>
                    @{u.username}
                  </Text>
                )}
              </div>
            </button>
          ))}

        {showKeepTyping && (
          <Text c="dimmed" size="sm" ta="center" py="xl">
            {t('chat.searchKeepTyping')}
          </Text>
        )}
        {showNoResults && (
          <Text c="dimmed" size="sm" ta="center" py="xl">
            {t('chat.noResults')}
          </Text>
        )}
      </div>

      <nav className="pheme-sidebar-tabs" aria-label={t('chat.sections')}>
        <button
          type="button"
          className="pheme-sidebar-tab"
          data-active={tab === 'chats'}
          aria-current={tab === 'chats'}
          onClick={() => setTab('chats')}
        >
          <IconMessages size={22} stroke={1.7} />
          <span>{t('chat.tabChats')}</span>
          {chatsUnread && <span className="pheme-tab-dot" aria-hidden />}
        </button>
        <button
          type="button"
          className="pheme-sidebar-tab"
          data-active={tab === 'channels'}
          aria-current={tab === 'channels'}
          onClick={() => setTab('channels')}
        >
          <IconSpeakerphone size={22} stroke={1.7} />
          <span>{t('chat.tabChannels')}</span>
          {channelsUnread && <span className="pheme-tab-dot" aria-hidden />}
        </button>
      </nav>
    </aside>
  )
}
