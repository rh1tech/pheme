import { useEffect, useState } from 'react'
import { Anchor, Breadcrumbs, Card, Container, Group, Stack, Text, Title } from '@mantine/core'
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { notifyError } from '../lib/notify'
import type { Channel, Message } from '../lib/types'
import { ImageCarousel } from '../components/ImageCarousel'
import { CardListSkeleton } from '../components/Skeletons'

export function MessagePage() {
  const { id = '', messageId = '' } = useParams()
  const { t } = useTranslation()
  const [message, setMessage] = useState<Message | null>(null)
  const [channel, setChannel] = useState<Channel | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let active = true
    const load = async () => {
      setLoading(true)
      try {
        const m = await api.getMessage(id, messageId)
        if (active) setMessage(m)
      } catch (e) {
        if (active) notifyError(t('dashboard.loadFailed'), e)
      } finally {
        if (active) setLoading(false)
      }
    }
    load()
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, messageId])

  useEffect(() => {
    let active = true
    api
      .listChannels()
      .then((cs) => active && setChannel(cs.find((c) => c.id === id) ?? null))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [id])

  return (
    <Container size="sm">
      <Stack gap="lg">
        <Breadcrumbs>
          <Anchor component={Link} to="/">
            {t('dashboard.yourChannels')}
          </Anchor>
          <Anchor component={Link} to={`/channels/${id}`}>
            {channel?.name ?? t('channel.fallbackName')}
          </Anchor>
          <Text>{t('channel.messageView')}</Text>
        </Breadcrumbs>

        {loading && <CardListSkeleton rows={1} />}

        {!loading && !message && (
          <Text c="dimmed" size="sm">
            {t('channel.messageNotFound')}
          </Text>
        )}

        {message && (
          <Card withBorder padding="lg">
            <Stack gap="sm">
              {message.images && message.images.length > 0 && <ImageCarousel images={message.images} />}
              <Group justify="space-between" align="flex-start" wrap="nowrap">
                <Title order={4}>{message.title || t('channel.noTitle')}</Title>
                <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>
                  {new Date(message.createdAt).toLocaleString()}
                </Text>
              </Group>
              {message.body && (
                <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>
                  {message.body}
                </Text>
              )}
            </Stack>
          </Card>
        )}
      </Stack>
    </Container>
  )
}
