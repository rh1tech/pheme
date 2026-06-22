import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Anchor,
  Breadcrumbs,
  Button,
  Card,
  Code,
  Container,
  Group,
  Stack,
  Table,
  Tabs,
  Text,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconTrash } from '@tabler/icons-react'
import { notifyError, notifySuccess } from '../lib/notify'
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { AdminChannel, ApiKey, Message } from '../lib/types'
import { ChannelStatusBadge } from '../components/badges'

export function AdminChannelPage() {
  const { id = '' } = useParams()
  const { t } = useTranslation()
  const [channel, setChannel] = useState<AdminChannel | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [cursor, setCursor] = useState('')
  const [loadingMore, setLoadingMore] = useState(false)
  const [keys, setKeys] = useState<ApiKey[]>([])

  useEffect(() => {
    let active = true
    // The admin channel list is the source of channel metadata (incl. owner email).
    api
      .adminListChannels('', 1, 100)
      .then((p) => active && setChannel(p.items.find((c) => c.id === id) ?? null))
      .catch(() => undefined)
    api
      .adminChannelMessages(id, '', '', 50)
      .then((page) => {
        if (!active) return
        setMessages(page.messages)
        setCursor(page.nextCursor)
      })
      .catch((e) => active && notifyError(t('admin.loadFailed'), e))
    api
      .adminListKeys(id)
      .then((ks) => active && setKeys(ks))
      .catch(() => undefined)
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  async function loadMore() {
    if (!cursor) return
    setLoadingMore(true)
    try {
      const page = await api.adminChannelMessages(id, cursor, '', 50)
      setMessages((prev) => [...prev, ...page.messages])
      setCursor(page.nextCursor)
    } catch (e) {
      notifyError(t('admin.loadFailed'), e)
    } finally {
      setLoadingMore(false)
    }
  }

  async function revokeKey(keyId: string) {
    try {
      await api.adminRevokeKey(id, keyId)
      setKeys(await api.adminListKeys(id))
      notifySuccess(t('admin.keyRevoked'))
    } catch (e) {
      notifyError(t('admin.revokeFailed'), e)
    }
  }

  const activeKeys = keys.filter((k) => !k.revokedAt)

  return (
    <Container size="md">
      <Stack gap="lg">
        <Breadcrumbs>
          <Anchor component={Link} to="/admin">
            {t('admin.title')}
          </Anchor>
          <Text>{channel?.name ?? t('channel.fallbackName')}</Text>
        </Breadcrumbs>

        <Card withBorder padding="lg">
          <Group justify="space-between" align="flex-start">
            <Stack gap={4}>
              <Title order={4}>{channel?.name ?? t('channel.fallbackName')}</Title>
              <Group gap="xs">
                <Text size="sm" c="dimmed">
                  {t('admin.owner')}:
                </Text>
                <Text size="sm">{channel?.ownerEmail}</Text>
              </Group>
              <Group gap="xs">
                <Text size="sm" c="dimmed">
                  {t('channel.triggerId')}
                </Text>
                <Code>{channel?.publicId ?? id}</Code>
              </Group>
            </Stack>
            {channel && <ChannelStatusBadge status={channel.status} />}
          </Group>
        </Card>

        <Tabs defaultValue="messages" keepMounted={false}>
          <Tabs.List mb="md">
            <Tabs.Tab value="messages">{t('admin.channelMessages')}</Tabs.Tab>
            <Tabs.Tab value="keys">{t('admin.channelKeys')}</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="messages">
            <Stack gap="sm">
              {messages.length === 0 && (
                <Text c="dimmed" size="sm">
                  {t('admin.noMessages')}
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
                    {t('admin.loadMore')}
                  </Button>
                </Group>
              )}
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="keys">
            {activeKeys.length === 0 ? (
              <Text c="dimmed" size="sm">
                {t('channel.noKeys')}
              </Text>
            ) : (
              <Table verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t('channel.colPrefix')}</Table.Th>
                    <Table.Th>{t('channel.colCreated')}</Table.Th>
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
                        <Tooltip label={t('admin.revoke')}>
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
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </Container>
  )
}
