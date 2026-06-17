import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Anchor,
  Badge,
  Breadcrumbs,
  Button,
  Card,
  Code,
  Container,
  CopyButton,
  Group,
  Modal,
  SegmentedControl,
  Stack,
  Table,
  Tabs,
  Text,
  TextInput,
  Textarea,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconSearch, IconTrash, IconX } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { ApiKey, Channel, CreatedKey, Message, SubscriptionMode } from '../lib/types'
import { useEventStream } from '../hooks/useEventStream'
import { loadWebDeviceId, saveWebDeviceId } from '../lib/device'
import { registerWebPushDevice, webPushSupported } from '../lib/webpush'

export function ChannelPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { t } = useTranslation()
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

  const [editName, setEditName] = useState('')
  const [editMode, setEditMode] = useState<SubscriptionMode>('approval')
  const [savingSettings, setSavingSettings] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)

  useEffect(() => {
    let active = true
    api
      .listChannels()
      .then((cs) => {
        if (!active) return
        const found = cs.find((c) => c.id === id) ?? null
        setChannel(found)
        if (found) {
          setEditName(found.name)
          setEditMode(found.subscriptionMode)
        }
      })
      .catch(() => active && setChannel(null))
    return () => {
      active = false
    }
  }, [id])

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
      .catch((e) => active && notifications.show({ color: 'red', message: `${t('dashboard.loadFailed')}: ${String(e)}` }))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
      notifications.show({ color: 'red', message: `${t('channel.searchFailed')}: ${String(e)}` })
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
      notifications.show({ color: 'red', message: `${t('dashboard.loadFailed')}: ${String(e)}` })
    } finally {
      setLoadingMore(false)
    }
  }

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
      notifications.show({ color: 'red', message: `${t('channel.keyFailed')}: ${String(e)}` })
    }
  }

  async function revokeKey(keyId: string) {
    try {
      await api.revokeKey(id, keyId)
      await reloadKeys()
      notifications.show({ color: 'green', message: t('channel.keyRevoked') })
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('channel.revokeFailed')}: ${String(e)}` })
    }
  }

  async function sendMessage() {
    if (!title.trim() && !body.trim()) return
    setSending(true)
    try {
      await api.notifyChannel(id, title.trim(), body.trim())
      setTitle('')
      setBody('')
      notifications.show({ color: 'green', message: t('channel.messageSent') })
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('channel.sendFailed')}: ${String(e)}` })
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
      notifications.show({ color: 'green', message: t('channel.browserSubscribed') })
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('channel.subscribeFailed')}: ${(e as Error).message}` })
    }
  }

  async function saveSettings() {
    setSavingSettings(true)
    try {
      const updated = await api.updateChannel(id, { name: editName.trim(), subscriptionMode: editMode })
      setChannel(updated)
      notifications.show({ color: 'green', message: t('channel.channelUpdated') })
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('channel.updateFailed')}: ${String(e)}` })
    } finally {
      setSavingSettings(false)
    }
  }

  async function deleteChannel() {
    setDeleting(true)
    try {
      await api.deleteChannel(id)
      notifications.show({ color: 'green', message: t('channel.channelDeleted') })
      navigate('/', { replace: true })
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('channel.deleteFailed')}: ${String(e)}` })
      setDeleting(false)
    }
  }

  const activeKeys = keys.filter((k) => !k.revokedAt)

  return (
    <Container size="sm">
      <Modal opened={createdKey !== null} onClose={() => setCreatedKey(null)} title={t('channel.keyCreatedTitle')}>
        <Stack>
          <Text size="sm" c="dimmed">
            {t('channel.keyShownOnce')}
          </Text>
          <Code block>{createdKey?.key}</Code>
          <Group justify="flex-end">
            <CopyButton value={createdKey?.key ?? ''}>
              {({ copied, copy }) => (
                <Button onClick={copy}>{copied ? t('common.copied') : t('channel.copyKey')}</Button>
              )}
            </CopyButton>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={confirmDelete} onClose={() => setConfirmDelete(false)} title={t('channel.deleteTitle')}>
        <Stack>
          <Text size="sm">
            <Trans
              i18nKey="channel.deleteConfirm"
              values={{ name: channel?.name ?? '' }}
              components={{ bold: <b /> }}
            />
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setConfirmDelete(false)}>
              {t('common.cancel')}
            </Button>
            <Button color="red" loading={deleting} onClick={deleteChannel}>
              {t('channel.deleteTitle')}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Stack gap="lg">
        <Breadcrumbs>
          <Anchor component={Link} to="/">
            {t('dashboard.yourChannels')}
          </Anchor>
          <Text>{channel?.name ?? t('channel.fallbackName')}</Text>
        </Breadcrumbs>

        <Card withBorder padding="lg">
          <Group justify="space-between" align="flex-start">
            <Stack gap={4}>
              <Title order={4}>{channel?.name ?? t('channel.fallbackName')}</Title>
              <Group gap="xs">
                <Text size="sm" c="dimmed">
                  {t('channel.triggerId')}
                </Text>
                <Code>{channel?.publicId ?? id}</Code>
                <CopyButton value={channel?.publicId ?? ''}>
                  {({ copied, copy }) => (
                    <Button size="compact-xs" variant="subtle" onClick={copy}>
                      {copied ? t('common.copied') : t('common.copy')}
                    </Button>
                  )}
                </CopyButton>
              </Group>
            </Stack>
            {channel && (
              <Badge color={channel.subscriptionMode === 'open' ? 'teal' : 'grape'}>
                {t(`mode.${channel.subscriptionMode}`)}
              </Badge>
            )}
          </Group>
        </Card>

        <Tabs defaultValue="messages" keepMounted={false}>
          <Tabs.List mb="md">
            <Tabs.Tab value="messages">{t('channel.tabs.messages')}</Tabs.Tab>
            <Tabs.Tab value="send">{t('channel.tabs.send')}</Tabs.Tab>
            <Tabs.Tab value="keys">{t('channel.tabs.keys')}</Tabs.Tab>
            <Tabs.Tab value="settings">{t('channel.tabs.settings')}</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="messages">
            <Stack gap="sm">
              <Group justify="flex-end">
                <TextInput
                  placeholder={t('channel.searchPlaceholder')}
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
              {activeQuery && (
                <Text size="xs" c="dimmed">
                  {t('channel.filtering', { query: activeQuery })}
                </Text>
              )}
              {messages.length === 0 && (
                <Text c="dimmed" size="sm">
                  {activeQuery ? t('channel.noMessagesSearch') : t('channel.noMessages')}
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
                    {t('channel.loadMore')}
                  </Button>
                </Group>
              )}
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="send">
            <Stack gap="sm">
              <TextInput
                label={t('channel.title')}
                placeholder={t('channel.titlePlaceholder')}
                value={title}
                onChange={(e) => setTitle(e.currentTarget.value)}
              />
              <Textarea
                label={t('channel.body')}
                placeholder={t('channel.bodyPlaceholder')}
                autosize
                minRows={3}
                value={body}
                onChange={(e) => setBody(e.currentTarget.value)}
              />
              <Group justify="flex-end">
                <Button onClick={sendMessage} loading={sending} disabled={!title.trim() && !body.trim()}>
                  {t('channel.send')}
                </Button>
              </Group>
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="keys">
            <Stack gap="sm">
              <Group justify="flex-end">
                <Button size="xs" variant="light" onClick={createKey}>
                  {t('channel.createKey')}
                </Button>
              </Group>
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
                          <Tooltip label={t('channel.revoke')}>
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
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="settings">
            <Stack gap="lg">
              <Stack gap="sm">
                <TextInput
                  label={t('dashboard.channelName')}
                  value={editName}
                  onChange={(e) => setEditName(e.currentTarget.value)}
                />
                <div>
                  <Text size="sm" fw={500} mb={4}>
                    {t('channel.subscriptionMode')}
                  </Text>
                  <SegmentedControl
                    value={editMode}
                    onChange={(v) => setEditMode(v as SubscriptionMode)}
                    data={[
                      { label: t('mode.approval'), value: 'approval' },
                      { label: t('mode.open'), value: 'open' },
                    ]}
                  />
                </div>
                <Group justify="flex-end">
                  <Button onClick={saveSettings} loading={savingSettings} disabled={!editName.trim()}>
                    {t('channel.saveChanges')}
                  </Button>
                </Group>
              </Stack>

              {webPushSupported() && (
                <Group>
                  <Button variant="light" onClick={subscribeBrowser}>
                    {t('channel.subscribeBrowser')}
                  </Button>
                </Group>
              )}

              <Card withBorder padding="md" style={{ borderColor: 'var(--mantine-color-red-4)' }}>
                <Group justify="space-between">
                  <Stack gap={2}>
                    <Text fw={600}>{t('channel.dangerTitle')}</Text>
                    <Text size="sm" c="dimmed">
                      {t('channel.dangerDescription')}
                    </Text>
                  </Stack>
                  <Button color="red" variant="outline" onClick={() => setConfirmDelete(true)}>
                    {t('common.delete')}
                  </Button>
                </Group>
              </Card>
            </Stack>
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </Container>
  )
}
