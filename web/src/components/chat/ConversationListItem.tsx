import { Group, Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { ChannelAvatar } from './ChannelAvatar'
import { chatListTime } from '../../lib/time'
import { conversationAvatarKey, conversationTitle, otherMember } from '../../lib/conversation'
import { useAuth } from '../../auth/context'
import type { ChatConversation } from '../../hooks/useConversationList'

interface ConversationListItemProps {
  item: ChatConversation
  active: boolean
  onSelect: (id: string) => void
}

export function ConversationListItem({ item, active, onSelect }: ConversationListItemProps) {
  const { t, i18n } = useTranslation()
  const { userId } = useAuth()
  const conv = item.conversation
  const title = conversationTitle(conv, userId ?? '')
  // A direct chat shows the other person's uploaded avatar; a group its own.
  const avatarId =
    conv.kind === 'direct' ? otherMember(conv, userId ?? '')?.avatarId : conv.avatarId

  return (
    <button
      type="button"
      className="pheme-chat-row"
      data-active={active}
      data-testid="conversation-row"
      onClick={() => onSelect(conv.id)}
    >
      <ChannelAvatar id={conversationAvatarKey(conv, userId ?? '')} name={title} avatarId={avatarId} />
      <Stack gap={2} style={{ minWidth: 0 }}>
        <Group gap="xs" wrap="nowrap" justify="space-between">
          <Text fw={600} size="sm" truncate>
            {title}
          </Text>
          {conv.lastMessage && (
            <Text className="pheme-chat-time" size="xs" style={{ whiteSpace: 'nowrap' }}>
              {chatListTime(item.lastActivity, i18n.language, t)}
            </Text>
          )}
        </Group>
        <Group gap="xs" wrap="nowrap" justify="space-between">
          <Text className="pheme-chat-preview" size="xs" truncate>
            {item.preview}
          </Text>
          {item.unread && (
            <span className="pheme-chat-unread" role="status" aria-label={t('chat.unreadChannel')} />
          )}
        </Group>
      </Stack>
    </button>
  )
}
