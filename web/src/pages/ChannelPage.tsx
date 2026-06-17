import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Code,
  Container,
  CopyButton,
  Group,
  Modal,
  Stack,
  Table,
  Text,
  TextInput,
  Textarea,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconSearch, IconTrash, IconX } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { useParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { ApiKey, Channel, CreatedKey, Message } from '../lib/types'
import { useEventStream } from '../hooks/useEventStream'
import { loadWebDeviceId, saveWebDeviceId } from '../lib/device'
import { registerWebPushDevice, webPushSupported } from '../lib/webpush'

export function ChannelPage() {
  const { id = '' } = useParams()
  const [channel, setChannel] = useState<Channel | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [cursor, setCursor] = useState('')
  const [loadingMore, setLoadingMore] = useState(false)
  const [search, setSearch] = useState('')
  const [activeQuery, setActiveQuery] = useState('')
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [createdKey, setCreatedKey] = useState<CreatedKey | null>(null)
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [sending, setSending] = useState(false)

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

  // First page of messages on channel change. setState runs in the promise
  // callback, the supported way to update from an effect.
  useEffect(() => {
    let active = true
    api
      .listMessages(id, '', '')
      .then((page) => {
        if (!active) return
        setActiveQuery('')
        setSearch('')
        setMessages(page.messages)
        setCursor(page.nextCursor)
      })
      .catch((e) => active && notifications.show({ color: 'red', message: `Load failed: ${String(e)}` }))
    return () => {
      active = false
    }
  }, [id])

  useEffect(() => {
    let active = true
    api
      .listKeys(id)
      .then((ks) => active && setKeys(ks))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [id])

  async function runSearch(query: string) {
    setActiveQuery(query)
    try {
      const page = await api.listMessages(id, '', query)
      setMessages(page.messages)
      setCursor(page.nextCursor)
    } catch (e) {
      notifications.show({ color: 'red', message: `Search failed: ${String(e)}` })
    }
  }

  async function loadMore() {
    if (!cursor) return
    setLoadingMore(true)
    try {
      const page = await api.listMessages(id, cursor, activeQuery)
      setMessages((prev) => [...prev, ...page.messages])
      setCursor(page.nextCursor)
    } catch (e) {
      notifications.show({ color: 'red', message: `Load failed: ${String(e)}` })
    } finally {
      setLoadingMore(false)
    }
  }

  // Live updates: prepend new messages, but only when no search filter is active
  // (a filtered view should not gain unrelated live messages).
  useEventStream((e) => {
    if (e.channelId !== id || activeQuery) return
    setMessages((prev) => (prev.some((m) => m.id === e.message.id) ? prev : [e.message, ...prev]))
  })

  async function reloadKeys() {
    try {
      setKeys(await api.listKeys(id))
    } catch {
      /* ignore */
    }
  }

  async function createKey() {
    try {
      setCreatedKey(await api.createKey(id))
      await reloadKeys()
    } catch (e) {
      notifications.show({ color: 'red', message: `Key failed: ${String(e)}` })
    }
  }

  async function revokeKey(keyId: string) {
    try {
      await api.revokeKey(id, keyId)
      await reloadKeys()
      notifications.show({ color: 'green', message: 'Key revoked' })
    } catch (e) {
      notifications.show({ color: 'red', message: `Revoke failed: ${String(e)}` })
    }
  }

  async function sendMessage() {
    if (!title.trim() && !body.trim()) return
    setSending(true)
    try {
      await api.notifyChannel(id, title.trim(), body.trim())
      setTitle('')
      setBody('')
      notifications.show({ color: 'green', message: 'Message sent' })
    } catch (e) {
      notifications.show({ color: 'red', message: `Send failed: ${String(e)}` })
    } finally {
      setSending(false)
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

  const activeKeys = keys.filter((k) => !k.revokedAt)

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
              {({ copied, copy }) => <Button onClick={copy}>{copied ? 'Copied' : 'Copy key'}</Button>}
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
          {webPushSupported() && (
            <Group mt="md">
              <Button size="xs" variant="light" onClick={subscribeBrowser}>
                Subscribe this browser
              </Button>
            </Group>
          )}
        </Card>

        <Card withBorder padding="lg">
          <Title order={5} mb="sm">
            Send a message
          </Title>
          <Stack gap="sm">
            <TextInput
              label="Title"
              placeholder="Deploy finished"
              value={title}
              onChange={(e) => setTitle(e.currentTarget.value)}
            />
            <Textarea
              label="Body"
              placeholder="Production deploy completed successfully."
              autosize
              minRows={2}
              value={body}
              onChange={(e) => setBody(e.currentTarget.value)}
            />
            <Group justify="flex-end">
              <Button onClick={sendMessage} loading={sending} disabled={!title.trim() && !body.trim()}>
                Send
              </Button>
            </Group>
          </Stack>
        </Card>

        <Card withBorder padding="lg">
          <Group justify="space-between" mb="sm">
            <Title order={5}>API keys</Title>
            <Button size="xs" variant="light" onClick={createKey}>
              Create key
            </Button>
          </Group>
          {activeKeys.length === 0 ? (
            <Text c="dimmed" size="sm">
              No active keys.
            </Text>
          ) : (
            <Table verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Prefix</Table.Th>
                  <Table.Th>Created</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {activeKeys.map((k) => (
                  <Table.Tr key={k.id}>
                    <Table.Td>
                      <Code>{k.prefix}…</Code>
                    </Table.Td>
                    <Table.Td>{new Date(k.createdAt).toLocaleDateString()}</Table.Td>
                    <Table.Td align="right">
                      <Tooltip label="Revoke">
                        <ActionIcon color="red" variant="subtle" onClick={() => revokeKey(k.id)}>
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Tooltip>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}
        </Card>

        <Stack gap="sm">
          <Group justify="space-between" align="flex-end">
            <Title order={5}>Messages</Title>
            <Group gap="xs">
              <TextInput
                placeholder="Search title or body"
                value={search}
                onChange={(e) => setSearch(e.currentTarget.value)}
                onKeyDown={(e) => e.key === 'Enter' && runSearch(search.trim())}
                rightSection={
                  activeQuery ? (
                    <ActionIcon
                      variant="subtle"
                      onClick={() => {
                        setSearch('')
                        runSearch('')
                      }}
                    >
                      <IconX size={16} />
                    </ActionIcon>
                  ) : (
                    <ActionIcon variant="subtle" onClick={() => runSearch(search.trim())}>
                      <IconSearch size={16} />
                    </ActionIcon>
                  )
                }
              />
            </Group>
          </Group>

          {activeQuery && (
            <Text size="xs" c="dimmed">
              Filtering by “{activeQuery}” — live updates paused.
            </Text>
          )}

          {messages.length === 0 && (
            <Text c="dimmed" size="sm">
              No messages{activeQuery ? ' match your search' : ' yet'}.
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
