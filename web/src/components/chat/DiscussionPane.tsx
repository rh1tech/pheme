import { useEffect, useState } from 'react'
import { ActionIcon, Divider, Stack, Text, Title } from '@mantine/core'
import { IconX } from '@tabler/icons-react'
import { useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { CommentsPanel } from '../CommentsPanel'
import { ImageCarousel } from '../ImageCarousel'
import { CardListSkeleton } from '../Skeletons'
import type { ChannelRelation, Message } from '../../lib/types'

/**
 * The third column: one message and its comments, Telegram's channel-discussion
 * view. It is the nested route `/channels/:id/messages/:messageId`, so the pane
 * is what a push notification's deep link opens — no separate page needed. The
 * caller's relation to the channel (which decides who may comment or moderate)
 * comes from the conversation's outlet context rather than a second fetch.
 */
export function DiscussionPane() {
  const { id = '', messageId = '' } = useParams()
  const relation = useOutletContext<ChannelRelation | null>()
  const { t } = useTranslation()
  const navigate = useNavigate()
  // The loaded message is stamped with the id it belongs to, so "loading" is
  // derived rather than a second state that an effect has to keep in step.
  const [loaded, setLoaded] = useState<{ messageId: string; message: Message | null } | null>(null)
  const loading = loaded?.messageId !== messageId
  const message = loaded?.messageId === messageId ? loaded.message : null

  useEffect(() => {
    let active = true
    const run = async () => {
      try {
        const m = await api.getMessage(id, messageId)
        if (active) setLoaded({ messageId, message: m })
      } catch (e) {
        if (!active) return
        setLoaded({ messageId, message: null })
        notifyError(t('dashboard.loadFailed'), e)
      }
    }
    void run()
    return () => {
      active = false
    }
  }, [id, messageId, t])

  return (
    <aside className="pheme-info" data-open="true" data-testid="discussion-pane">
      <div className="pheme-info-header">
        <Title order={6} style={{ flex: 1 }}>
          {t('channel.discussion')}
        </Title>
        <ActionIcon
          variant="subtle"
          color="gray"
          aria-label={t('channel.closeInfo')}
          onClick={() => navigate(`/channels/${id}`)}
        >
          <IconX size={18} />
        </ActionIcon>
      </div>

      <div className="pheme-info-body">
        {loading && <CardListSkeleton rows={1} />}

        {!loading && !message && (
          <Text c="dimmed" size="sm">
            {t('channel.messageNotFound')}
          </Text>
        )}

        {message && (
          <Stack gap="sm">
            <Stack gap="xs">
              {message.images && message.images.length > 0 && (
                <ImageCarousel images={message.images} />
              )}
              {message.title && <Text fw={600}>{message.title}</Text>}
              {message.body && (
                <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>
                  {message.body}
                </Text>
              )}
            </Stack>

            <Divider />

            <CommentsPanel
              channelId={id}
              messageId={messageId}
              commentsAllowed={message.commentsAllowed}
              canComment={relation?.status === 'active'}
              canModerate={relation ? relation.isOwner || relation.role === 'admin' : false}
              autoFocus
            />
          </Stack>
        )}
      </div>
    </aside>
  )
}
