import { useMemo, useState } from 'react'
import { Group, Stack, Text, TextInput } from '@mantine/core'
import { IconSearch } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ChatSidebarMenu } from './ChatSidebarMenu'
import { NewChannelMenu } from './NewChannelMenu'
import { ChatListItem } from './ChatListItem'
import { NotificationsBanner } from '../NotificationsBanner'
import { CardListSkeleton } from '../Skeletons'
import type { ChannelListApi } from '../../hooks/useChannelList'

interface ChatSidebarProps {
  list: ChannelListApi
  /** The channel currently open in the conversation pane, if any. */
  activeId?: string
  /** Fired on every pick, including a re-pick of the channel already open. */
  onSelectChannel: () => void
}

export function ChatSidebar({ list, activeId, onSelectChannel }: ChatSidebarProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')

  // The whole list is already in memory, so filtering is local — no round trip.
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return list.channels
    return list.channels.filter(
      (c) => c.name.toLowerCase().includes(q) || (c.alias ?? '').toLowerCase().includes(q),
    )
  }, [list.channels, query])

  const empty = !list.loading && list.channels.length === 0
  const noMatches = !list.loading && list.channels.length > 0 && visible.length === 0

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
          <NewChannelMenu onChanged={list.refresh} />
        </Group>
      </div>

      <div className="pheme-sidebar-list">
        <NotificationsBanner />

        {list.loading && <CardListSkeleton rows={5} />}

        {(empty || noMatches) && (
          <Stack align="center" py="xl">
            <Text c="dimmed" size="sm">
              {empty ? t('chat.noChannels') : t('chat.noResults')}
            </Text>
          </Stack>
        )}

        {visible.map((c) => (
          <ChatListItem
            key={c.id}
            channel={c}
            active={c.id === activeId}
            onSelect={(id) => {
              onSelectChannel()
              navigate(`/channels/${id}`)
            }}
          />
        ))}
      </div>
    </aside>
  )
}
