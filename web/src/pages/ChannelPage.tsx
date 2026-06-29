import { useEffect, useRef, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Breadcrumbs,
  Button,
  Card,
  Code,
  Container,
  CopyButton,
  Group,
  Image,
  Modal,
  SegmentedControl,
  Stack,
  Switch,
  Table,
  Tabs,
  Text,
  TextInput,
  Textarea,
  Title,
  Tooltip,
} from '@mantine/core'
import {
  IconBellCheck,
  IconDeviceMobile,
  IconLogout,
  IconPhoto,
  IconSearch,
  IconSend,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import { QRCodeSVG } from 'qrcode.react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { api, imageUrl } from '../lib/api'
import { notifyError, notifySuccess } from '../lib/notify'
import type { ApiKey, ChannelRole, Channel, CreatedKey, Message, SubscriptionMode } from '../lib/types'
import { useEventStream } from '../hooks/useEventStream'
import { ConfirmModal } from '../components/ConfirmModal'
import { ModeBadge } from '../components/badges'
import { SubscribersPanel } from '../components/SubscribersPanel'
import { CardListSkeleton } from '../components/Skeletons'
import { PullToRefresh } from '../components/PullToRefresh'
import { ResponsiveModal } from '../components/ResponsiveModal'
import { ImagePicker } from '../components/ImagePicker'
import { BRAND_GRADIENT } from '../theme'
import { loadWebDeviceId, saveWebDeviceId } from '../lib/device'
import { registerWebPushDevice, webPushAvailability } from '../lib/webpush'

// Keep these in sync with the server limits (internal/channel/notify_input.go).
const MAX_IMAGES = 10
const MAX_IMAGE_BYTES = 10 * 1024 * 1024

export function ChannelPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [channel, setChannel] = useState<Channel | null>(null)
  const [isOwner, setIsOwner] = useState(false)
  const [myRole, setMyRole] = useState<ChannelRole | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [loadingMessages, setLoadingMessages] = useState(true)
  const [cursor, setCursor] = useState('')
  const [loadingMore, setLoadingMore] = useState(false)
  const [search, setSearch] = useState('')
  const [activeQuery, setActiveQuery] = useState('')
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [createdKey, setCreatedKey] = useState<CreatedKey | null>(null)
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [images, setImages] = useState<File[]>([])
  const [allowComments, setAllowComments] = useState(true)
  const [sending, setSending] = useState(false)
  const [sendOpen, setSendOpen] = useState(false)
  const titleRef = useRef<HTMLInputElement>(null)

  const [editName, setEditName] = useState('')
  const [editMode, setEditMode] = useState<SubscriptionMode>('approval')
  const [editAlias, setEditAlias] = useState('')
  const [savingSettings, setSavingSettings] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirmLeave, setConfirmLeave] = useState(false)
  const [leaving, setLeaving] = useState(false)
  const [subStatus, setSubStatus] = useState<'active' | 'pending' | 'none'>('none')
  const [subBusy, setSubBusy] = useState(false)
  const pushAvailability = webPushAvailability()
  const canModerate = isOwner || myRole === 'admin'
  // Absolute deep link encoded in the share QR (resolved by the /join route).
  const shareRef = channel?.alias || channel?.publicId || id
  const shareUrl = `${window.location.origin}/join?ref=${encodeURIComponent(shareRef)}`

  useEffect(() => {
    let active = true
    api
      .getChannel(id)
      .then((rel) => {
        if (!active) return
        setChannel(rel.channel)
        setIsOwner(rel.isOwner)
        setMyRole(rel.role)
        setEditName(rel.channel.name)
        setEditMode(rel.channel.subscriptionMode)
        setEditAlias(rel.channel.alias ?? '')
      })
      .catch(() => active && setChannel(null))
    return () => {
      active = false
    }
  }, [id])

  useEffect(() => {
    let active = true
    const load = async () => {
      setLoadingMessages(true)
      try {
        const page = await api.listMessages(id, '', '')
        if (!active) return
        setActiveQuery('')
        setSearch('')
        setMessages(page.messages)
        setCursor(page.nextCursor)
      } catch (e) {
        if (active) notifyError(t('dashboard.loadFailed'), e)
      } finally {
        if (active) setLoadingMessages(false)
      }
    }
    load()
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  useEffect(() => {
    if (!isOwner) return
    let active = true
    api
      .listKeys(id)
      .then((ks) => active && setKeys(ks))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [id, isOwner])

  // Load whether this browser's device is subscribed to the channel.
  useEffect(() => {
    let active = true
    const deviceId = loadWebDeviceId()
    if (!deviceId) return // no device registered → status stays "none"
    api
      .channelSubscription(id, deviceId)
      .then((status) => active && setSubStatus(status))
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
      notifyError(t('channel.searchFailed'), e)
    }
  }

  async function refreshMessages() {
    try {
      const page = await api.listMessages(id, '', activeQuery)
      setMessages(page.messages)
      setCursor(page.nextCursor)
    } catch (e) {
      notifyError(t('dashboard.loadFailed'), e)
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
      notifyError(t('dashboard.loadFailed'), e)
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
      notifyError(t('channel.keyFailed'), e)
    }
  }

  async function revokeKey(keyId: string) {
    try {
      await api.revokeKey(id, keyId)
      await reloadKeys()
      notifySuccess(t('channel.keyRevoked'))
    } catch (e) {
      notifyError(t('channel.revokeFailed'), e)
    }
  }

  function addImages(selected: File[]) {
    if (selected.length === 0) return
    const tooBig = selected.find((f) => f.size > MAX_IMAGE_BYTES)
    if (tooBig) {
      notifyError(t('channel.imageTooLarge', { name: tooBig.name }))
      return
    }
    setImages((prev) => {
      const next = [...prev, ...selected]
      if (next.length > MAX_IMAGES) {
        notifyError(t('channel.tooManyImages', { max: MAX_IMAGES }))
        return next.slice(0, MAX_IMAGES)
      }
      return next
    })
  }

  function removeImage(index: number) {
    setImages((prev) => prev.filter((_, i) => i !== index))
  }

  const canSend = title.trim().length > 0 || body.trim().length > 0 || images.length > 0

  async function sendMessage() {
    if (!canSend) return
    setSending(true)
    try {
      await api.notifyChannel(id, title.trim(), body.trim(), images, allowComments)
      setTitle('')
      setBody('')
      setImages([])
      setAllowComments(true)
      setSendOpen(false)
      notifySuccess(t('channel.messageSent'))
    } catch (e) {
      notifyError(t('channel.sendFailed'), e)
    } finally {
      setSending(false)
    }
  }

  async function subscribeBrowser() {
    setSubBusy(true)
    try {
      // Always (re)register: the server upserts the web device by its push
      // endpoint, so this self-heals a stale cached id or a deleted device.
      const deviceId = await registerWebPushDevice()
      saveWebDeviceId(deviceId)
      await api.subscribe(id, deviceId)
      const status = await api.channelSubscription(id, deviceId)
      setSubStatus(status)
      notifySuccess(t('channel.browserSubscribed'))
    } catch (e) {
      notifyError(t('channel.subscribeFailed'), e)
    } finally {
      setSubBusy(false)
    }
  }

  async function unsubscribeBrowser() {
    const deviceId = loadWebDeviceId()
    if (!deviceId) {
      setSubStatus('none')
      return
    }
    setSubBusy(true)
    try {
      await api.unsubscribe(id, deviceId)
      setSubStatus('none')
      notifySuccess(t('channel.unsubscribed'))
    } catch (e) {
      notifyError(t('channel.unsubscribeFailed'), e)
    } finally {
      setSubBusy(false)
    }
  }

  async function saveSettings() {
    setSavingSettings(true)
    try {
      const updated = await api.updateChannel(id, {
        name: editName.trim(),
        subscriptionMode: editMode,
        alias: editAlias.trim(),
      })
      setChannel(updated)
      setEditAlias(updated.alias ?? '')
      notifySuccess(t('channel.channelUpdated'))
    } catch (e) {
      notifyError(t('channel.updateFailed'), e)
    } finally {
      setSavingSettings(false)
    }
  }

  async function leaveChannel() {
    setLeaving(true)
    try {
      await api.leaveChannel(id)
      notifySuccess(t('channel.left'))
      navigate('/', { replace: true })
    } catch (e) {
      notifyError(t('channel.leaveFailed'), e)
      setLeaving(false)
    }
  }

  async function deleteChannel() {
    setDeleting(true)
    try {
      await api.deleteChannel(id)
      notifySuccess(t('channel.channelDeleted'))
      navigate('/', { replace: true })
    } catch (e) {
      notifyError(t('channel.deleteFailed'), e)
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

      <ConfirmModal
        opened={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={deleteChannel}
        title={t('channel.deleteTitle')}
        confirmLabel={t('channel.deleteTitle')}
        loading={deleting}
      >
        <Text size="sm">
          <Trans i18nKey="channel.deleteConfirm" values={{ name: channel?.name ?? '' }} components={{ bold: <b /> }} />
        </Text>
      </ConfirmModal>

      <ConfirmModal
        opened={confirmLeave}
        onClose={() => setConfirmLeave(false)}
        onConfirm={leaveChannel}
        title={t('channel.leave')}
        confirmLabel={t('channel.leave')}
        loading={leaving}
      >
        <Text size="sm">
          <Trans i18nKey="channel.leaveConfirm" values={{ name: channel?.name ?? '' }} components={{ bold: <b /> }} />
        </Text>
      </ConfirmModal>

      {canModerate && (
        <ResponsiveModal
          opened={sendOpen}
          onClose={() => setSendOpen(false)}
          title={t('channel.tabs.send')}
          size="lg"
          onEnterTransitionEnd={() => titleRef.current?.focus()}
        >
          <Stack gap="sm">
            <TextInput
              ref={titleRef}
              data-autofocus
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

            <ImagePicker files={images} max={MAX_IMAGES} onAdd={addImages} onRemove={removeImage} />

            <Switch
              checked={allowComments}
              onChange={(e) => setAllowComments(e.currentTarget.checked)}
              label={t('channel.allowComments')}
              description={t('channel.allowCommentsHint')}
            />

            <Group justify="flex-end">
              <Button variant="default" onClick={() => setSendOpen(false)}>
                {t('common.cancel')}
              </Button>
              <Button onClick={sendMessage} loading={sending} disabled={!canSend}>
                {t('channel.send')}
              </Button>
            </Group>
          </Stack>
        </ResponsiveModal>
      )}

      <Stack gap="lg">
        <Breadcrumbs>
          <Anchor component={Link} to="/">
            {t('dashboard.yourChannels')}
          </Anchor>
          <Text>{channel?.name ?? t('channel.fallbackName')}</Text>
        </Breadcrumbs>

        <Group justify="space-between" align="center" wrap="nowrap">
          <Group gap="xs" align="center">
            <Title order={2}>{channel?.name ?? t('channel.fallbackName')}</Title>
            {channel?.alias && (
              <Text size="sm" c="dimmed">
                @{channel.alias}
              </Text>
            )}
          </Group>
          {channel && <ModeBadge mode={channel.subscriptionMode} />}
        </Group>

        <Tabs defaultValue="messages" keepMounted={false}>
          <Tabs.List mb="md">
            <Tabs.Tab value="messages">{t('channel.tabs.messages')}</Tabs.Tab>
            <Tabs.Tab value="settings">{t('channel.tabs.settings')}</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="messages">
            <PullToRefresh onRefresh={refreshMessages}>
            <Stack gap="sm">
              <Group justify="flex-end" wrap="wrap" gap="sm">
                {canModerate && (
                  <Button
                    visibleFrom="sm"
                    mr="auto"
                    leftSection={<IconSend size={16} />}
                    onClick={() => setSendOpen(true)}
                  >
                    {t('channel.send')}
                  </Button>
                )}
                <TextInput
                  placeholder={t('channel.searchPlaceholder')}
                  value={search}
                  onChange={(e) => setSearch(e.currentTarget.value)}
                  onKeyDown={(e) => e.key === 'Enter' && runSearch(search.trim())}
                  leftSection={<IconSearch size={16} stroke={1.8} />}
                  rightSection={
                    activeQuery || search ? (
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={t('common.clear')}
                        onClick={() => {
                          setSearch('')
                          runSearch('')
                        }}
                      >
                        <IconX size={16} />
                      </ActionIcon>
                    ) : null
                  }
                  w={260}
                />
              </Group>
              {activeQuery && (
                <Text size="xs" c="dimmed">
                  {t('channel.filtering', { query: activeQuery })}
                </Text>
              )}
              {loadingMessages && <CardListSkeleton rows={4} />}
              {!loadingMessages && messages.length === 0 && (
                <Text c="dimmed" size="sm">
                  {activeQuery ? t('channel.noMessagesSearch') : t('channel.noMessages')}
                </Text>
              )}
              {!loadingMessages &&
                messages.map((m) => (
                  <Card
                    key={m.id}
                    withBorder
                    padding="sm"
                    component={Link}
                    to={`/channels/${id}/messages/${m.id}`}
                    className="pheme-card"
                    data-clickable="true"
                  >
                    <Stack gap="xs">
                      {m.images && m.images.length > 0 && (
                        <div style={{ position: 'relative' }}>
                          <Image
                            src={imageUrl(m.images[0].id)}
                            alt={m.title || t('channel.noTitle')}
                            h={160}
                            radius="sm"
                            fit="cover"
                          />
                          {m.images.length > 1 && (
                            <Badge
                              variant="filled"
                              color="dark"
                              leftSection={<IconPhoto size={12} />}
                              style={{ position: 'absolute', top: 8, right: 8 }}
                            >
                              {m.images.length}
                            </Badge>
                          )}
                        </div>
                      )}
                      <Group justify="space-between" align="flex-start" wrap="nowrap">
                        <Text fw={600}>{m.title || t('channel.noTitle')}</Text>
                        <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>
                          {new Date(m.createdAt).toLocaleString()}
                        </Text>
                      </Group>
                      {m.body && (
                        <Text size="sm" lineClamp={2}>
                          {m.body}
                        </Text>
                      )}
                    </Stack>
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
            </PullToRefresh>
          </Tabs.Panel>

          <Tabs.Panel value="settings">
            <Stack gap="lg">
              {isOwner && (
                <>
                  <Card withBorder padding="md">
                    <Stack gap="sm">
                      <Text fw={600}>{t('channel.detailsTitle')}</Text>
                      <TextInput
                        label={t('dashboard.channelName')}
                        value={editName}
                        onChange={(e) => setEditName(e.currentTarget.value)}
                      />
                      <TextInput
                        label={t('channel.phetagLabel')}
                        placeholder={t('channel.phetagPlaceholder')}
                        description={t('channel.phetagHint')}
                        leftSection={<Text size="sm" c="dimmed">@</Text>}
                        value={editAlias}
                        onChange={(e) => setEditAlias(e.currentTarget.value)}
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
                  </Card>

                  <Card withBorder padding="md">
                    <Stack gap="sm" align="center">
                      <Stack gap={2} align="center">
                        <Text fw={600}>{t('channel.shareTitle')}</Text>
                        <Text size="sm" c="dimmed" ta="center">
                          {t('channel.shareDescription')}
                        </Text>
                      </Stack>
                      <div style={{ background: '#fff', padding: 12, borderRadius: 8 }}>
                        <QRCodeSVG value={shareUrl} size={168} />
                      </div>
                      <Stack gap="xs" align="center">
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
                        {channel?.alias && (
                          <Group gap="xs">
                            <Text size="sm" c="dimmed">
                              {t('channel.phetag')}
                            </Text>
                            <Code>@{channel.alias}</Code>
                            <CopyButton value={channel.alias}>
                              {({ copied, copy }) => (
                                <Button size="compact-xs" variant="subtle" onClick={copy}>
                                  {copied ? t('common.copied') : t('common.copy')}
                                </Button>
                              )}
                            </CopyButton>
                          </Group>
                        )}
                      </Stack>
                    </Stack>
                  </Card>
                </>
              )}

              {canModerate && (
                <Card withBorder padding="md">
                  <Stack gap="sm">
                    <Text fw={600}>{t('channel.tabs.subscribers')}</Text>
                    <SubscribersPanel channelId={id} />
                  </Stack>
                </Card>
              )}

              {isOwner && (
                <Card withBorder padding="md">
                  <Group justify="space-between" mb="sm">
                    <Text fw={600}>{t('channel.tabs.keys')}</Text>
                    <Button size="xs" variant="light" onClick={createKey}>
                      {t('channel.createKey')}
                    </Button>
                  </Group>
                  {activeKeys.length === 0 ? (
                    <Text c="dimmed" size="sm">
                      {t('channel.noKeys')}
                    </Text>
                  ) : (
                    <Table.ScrollContainer minWidth={420}>
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
                    </Table.ScrollContainer>
                  )}
                </Card>
              )}

              <Card withBorder padding="md">
                <Group justify="space-between">
                  <Stack gap={2}>
                    <Group gap="xs">
                      <Text fw={600}>{t('channel.subscribeTitle')}</Text>
                      {pushAvailability === 'supported' && subStatus === 'active' && (
                        <Badge color="teal" variant="light" leftSection={<IconBellCheck size={14} />}>
                          {t('channel.subscribed')}
                        </Badge>
                      )}
                      {pushAvailability === 'supported' && subStatus === 'pending' && (
                        <Badge color="yellow" variant="light">
                          {t('channel.subscriptionPending')}
                        </Badge>
                      )}
                    </Group>
                    <Text size="sm" c="dimmed">
                      {t('channel.subscribeDescription')}
                    </Text>
                  </Stack>
                  {pushAvailability === 'supported' &&
                    (subStatus === 'none' ? (
                      <Button variant="outline" loading={subBusy} onClick={subscribeBrowser}>
                        {t('channel.subscribeBrowser')}
                      </Button>
                    ) : (
                      <Button variant="subtle" color="red" loading={subBusy} onClick={unsubscribeBrowser}>
                        {t('channel.unsubscribe')}
                      </Button>
                    ))}
                </Group>
                {pushAvailability !== 'supported' && (
                  <Alert
                    mt="sm"
                    variant="light"
                    color={pushAvailability === 'ios-needs-install' ? 'blue' : 'gray'}
                    icon={<IconDeviceMobile size={18} />}
                  >
                    {pushAvailability === 'ios-needs-install'
                      ? t('channel.subscribeIosHint')
                      : t('channel.subscribeUnsupported')}
                  </Alert>
                )}
              </Card>

              {isOwner ? (
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
              ) : (
                <Card withBorder padding="md" style={{ borderColor: 'var(--mantine-color-red-4)' }}>
                  <Group justify="space-between">
                    <Stack gap={2}>
                      <Text fw={600}>{t('channel.leaveTitle')}</Text>
                      <Text size="sm" c="dimmed">
                        {t('channel.leaveDescription')}
                      </Text>
                    </Stack>
                    <Button
                      color="red"
                      variant="outline"
                      leftSection={<IconLogout size={16} />}
                      onClick={() => setConfirmLeave(true)}
                    >
                      {t('channel.leave')}
                    </Button>
                  </Group>
                </Card>
              )}
            </Stack>
          </Tabs.Panel>
        </Tabs>
      </Stack>

      {canModerate && (
        <ActionIcon
          aria-label={t('channel.send')}
          onClick={() => setSendOpen(true)}
          hiddenFrom="sm"
          variant="gradient"
          gradient={BRAND_GRADIENT}
          radius="xl"
          size={56}
          style={{ position: 'fixed', right: 20, bottom: 20, zIndex: 200, boxShadow: 'var(--mantine-shadow-lg)' }}
        >
          <IconSend size={24} />
        </ActionIcon>
      )}
    </Container>
  )
}
