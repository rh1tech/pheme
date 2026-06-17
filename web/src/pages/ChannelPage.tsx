import { useEffect, useState } from 'react'
import {
  Badge,
  Button,
  Card,
  Code,
  Container,
  CopyButton,
  Group,
  Modal,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { Channel, CreatedKey, Message } from '../lib/types'
import { useEventStream } from '../hooks/useEventStream'
import { loadWebDeviceId, saveWebDeviceId } from '../lib/device'
import { registerWebPushDevice, webPushSupported } from '../lib/webpush'

export function ChannelPage() {
  const { id = '' } = useParams()
  const [channel, setChannel] = useState<Channel | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [cursor, setCursor] = useState('')
  const [loadingMore, setLoadingMore] = useState(false)
  const [createdKey, setCreatedKey] = useState<CreatedKey | null>(null)

  useEffect(() => {
    let active = true
    api
      .listChannels()
      .then((cs) => active && setChannel(cs.find((c) => c.id === id) ?? null))
      .catch(() => active && setChannel(null))
    return () => {
      active = false
    }
  }, [id])

  // Load the first page whenever the channel changes. setState runs in the
  // promise callback, which is the supported way to update from an effect.
  useEffect(() => {
    let active = true
    api
      .listMessages(id, '')
      .then((page) => {
        if (!active) return
        setMessages(page.messages)
        setCursor(page.nextCursor)
      })
      .catch((e) => active && notifications.show({ color: 'red', message: `Load failed: ${String(e)}` }))
    return () => {
      active = false
    }
  }, [id])

  async function loadMore() {
    if (!cursor) return
    setLoadingMore(true)
    try {
      const page = await api.listMessages(id, cursor)
      setMessages((prev) => [...prev, ...page.messages])
      setCursor(page.nextCursor)
    } catch (e) {
      notifications.show({ color: 'red', message: `Load failed: ${String(e)}` })
    } finally {
      setLoadingMore(false)
    }
  }

  // Live updates: prepend messages for this channel as they arrive.
  useEventStream((e) => {
    if (e.channelId !== id) return
    setMessages((prev) => (prev.some((m) => m.id === e.message.id) ? prev : [e.message, ...prev]))
  })

  async function createKey() {
    try {
      setCreatedKey(await api.createKey(id))
    } catch (e) {
      notifications.show({ color: 'red', message: `Key failed: ${String(e)}` })
    }
  }

  async function subscribeBrowser() {
    try {
      let deviceId = loadWebDeviceId()
      if (!deviceId) {
        deviceId = await registerWebPushDevice()
        saveWebDeviceId(deviceId)
      }
      await api.subscribe(id, deviceId)
      notifications.show({ color: 'green', message: 'This browser is subscribed' })
    } catch (e) {
      notifications.show({ color: 'red', message: `Subscribe failed: ${(e as Error).message}` })
    }
  }

  return (
    <Container size="sm">
      <Modal opened={createdKey !== null} onClose={() => setCreatedKey(null)} title="API key created">
        <Stack>
          <Text size="sm" c="dimmed">
            Store this key now — it will not be shown again.
          </Text>
          <Code block>{createdKey?.key}</Code>
          <Group justify="flex-end">
            <CopyButton value={createdKey?.key ?? ''}>
              {({ copied, copy }) => (
                <Button onClick={copy}>{copied ? 'Copied' : 'Copy key'}</Button>
              )}
            </CopyButton>
          </Group>
        </Stack>
      </Modal>

      <Stack gap="lg">
        <Card withBorder padding="lg">
          <Group justify="space-between" align="flex-start">
            <Stack gap={4}>
              <Title order={4}>{channel?.name ?? 'Channel'}</Title>
              <Group gap="xs">
                <Text size="sm" c="dimmed">
                  Trigger ID:
                </Text>
                <Code>{channel?.publicId ?? id}</Code>
                <CopyButton value={channel?.publicId ?? ''}>
                  {({ copied, copy }) => (
                    <Button size="compact-xs" variant="subtle" onClick={copy}>
                      {copied ? 'Copied' : 'Copy'}
                    </Button>
                  )}
                </CopyButton>
              </Group>
            </Stack>
            {channel && (
              <Badge color={channel.subscriptionMode === 'open' ? 'teal' : 'grape'}>
                {channel.subscriptionMode}
              </Badge>
            )}
          </Group>
          <Group mt="md">
            <Button size="xs" variant="light" onClick={createKey}>
              Create API key
            </Button>
            {webPushSupported() && (
              <Button size="xs" variant="light" onClick={subscribeBrowser}>
                Subscribe this browser
              </Button>
            )}
          </Group>
        </Card>

        <Stack gap="sm">
          <Title order={5}>Messages</Title>
          {messages.length === 0 && (
            <Text c="dimmed" size="sm">
              No messages yet.
            </Text>
          )}
          {messages.map((m) => (
            <Card key={m.id} withBorder padding="sm">
              <Group justify="space-between" align="flex-start">
                <Text fw={600}>{m.title || '(no title)'}</Text>
                <Text size="xs" c="dimmed">
                  {new Date(m.createdAt).toLocaleString()}
                </Text>
              </Group>
              {m.body && <Text size="sm">{m.body}</Text>}
            </Card>
          ))}
          {cursor && (
            <Group justify="center">
              <Button variant="subtle" loading={loadingMore} onClick={loadMore}>
                Load more
              </Button>
            </Group>
          )}
        </Stack>
      </Stack>
    </Container>
  )
}
