import { Group, Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { ChannelAvatar } from './ChannelAvatar'
import { chatListTime } from '../../lib/time'
import type { ChatChannel } from '../../hooks/useChannelList'

interface ChatListItemProps {
  channel: ChatChannel
  active: boolean
  onSelect: (id: string) => void
}

export function ChatListItem({ channel, active, onSelect }: ChatListItemProps) {
  const { t, i18n } = useTranslation()
  const last = channel.lastMessage

  // Preview line, in Telegram's order of preference: the message's own text, or
  // a photo count when it carried only images, or the channel's handle when the
  // channel has never been notified.
  let preview = ''
  if (last) {
    preview = last.title || last.body
    if (!preview && last.imageCount > 0) {
      preview =
        last.imageCount === 1 ? t('chat.photo') : t('chat.photos', { count: last.imageCount })
    }
  } else if (channel.alias) {
    preview = `@${channel.alias}`
  }

  return (
    <button
      type="button"
      className="pheme-chat-row"
      data-active={active}
      data-testid="chat-row"
      onClick={() => onSelect(channel.id)}
    >
      <ChannelAvatar id={channel.id} name={channel.name} avatarId={channel.avatarId} />
      <Stack gap={2} style={{ minWidth: 0 }}>
        <Group gap="xs" wrap="nowrap" justify="space-between">
          <Text fw={600} size="sm" truncate>
            {channel.name}
          </Text>
          {last && (
            <Text className="pheme-chat-time" size="xs" style={{ whiteSpace: 'nowrap' }}>
              {chatListTime(last.createdAt, i18n.language, t)}
            </Text>
          )}
        </Group>
        <Group gap="xs" wrap="nowrap" justify="space-between">
          <Text className="pheme-chat-preview" size="xs" truncate>
            {preview}
          </Text>
          {channel.unread && (
            <span
              className="pheme-chat-unread"
              role="status"
              aria-label={t('chat.unreadChannel')}
            />
          )}
        </Group>
      </Stack>
    </button>
  )
}
