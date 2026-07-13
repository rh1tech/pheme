import { Group, Text } from '@mantine/core'
import { IconMessageCircle } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { ImageCarousel } from '../ImageCarousel'
import { messageTime } from '../../lib/time'
import type { Message } from '../../lib/types'

interface MessageBubbleProps {
  message: Message
  /** True when this message's discussion pane is open. */
  active: boolean
  onOpenDiscussion: (messageId: string) => void
}

export function MessageBubble({ message, active, onOpenDiscussion }: MessageBubbleProps) {
  const { t, i18n } = useTranslation()
  const count = message.commentCount ?? 0
  const hasImages = (message.images?.length ?? 0) > 0

  return (
    <article
      className="pheme-bubble"
      data-active={active}
      data-testid="message-bubble"
      data-message-id={message.id}
    >
      {hasImages && (
        <div className="pheme-bubble-media">
          <ImageCarousel images={message.images ?? []} />
        </div>
      )}

      {message.title && (
        <Text fw={600} size="sm">
          {message.title}
        </Text>
      )}

      {message.body && (
        <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>
          {message.body}
        </Text>
      )}

      <div className="pheme-bubble-footer">
        {message.commentsAllowed && (
          <button
            type="button"
            className="pheme-comment-chip"
            // The chip shows a bare count, as Telegram's does. The full phrase
            // lives in the accessible name, where the number alone would be
            // meaningless read aloud.
            aria-label={count > 0 ? t('channel.commentCount', { count }) : t('channel.comment')}
            onClick={() => onOpenDiscussion(message.id)}
          >
            <IconMessageCircle size={14} />
            {count > 0 ? count : t('channel.comment')}
          </button>
        )}
        <Group gap={4} wrap="nowrap">
          <Text size="xs" c="dimmed">
            {messageTime(message.createdAt, i18n.language)}
          </Text>
        </Group>
      </div>
    </article>
  )
}
