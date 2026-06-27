import { useEffect, useRef, useState } from 'react'
import {
  Badge,
  Button,
  Card,
  Container,
  Group,
  Modal,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { IconBellCheck } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { notifyError, notifySuccess } from '../lib/notify'
import type { Channel, JoinedChannel, SubscriptionMode } from '../lib/types'
import { getWebPushState, registerWebPushDevice, webPushSupported } from '../lib/webpush'
import { saveWebDeviceId } from '../lib/device'
import { ChannelRoleBadge, MemberStatusBadge, ModeBadge } from '../components/badges'
import { CardListSkeleton } from '../components/Skeletons'

export function DashboardPage() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [channels, setChannels] = useState<Channel[]>([])
  const [joined, setJoined] = useState<JoinedChannel[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [name, setName] = useState('')
  const [mode, setMode] = useState<SubscriptionMode>('approval')
  const [creating, setCreating] = useState(false)
  const [joinOpen, setJoinOpen] = useState(false)
  const [joinRef, setJoinRef] = useState('')
  const [joining, setJoining] = useState(false)
  const [pushOn, setPushOn] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)
  const joinRefInput = useRef<HTMLInputElement>(null)

  async function refresh() {
    try {
      const [owned, mine] = await Promise.all([api.listChannels(), api.listJoinedChannels()])
      setChannels(owned)
      setJoined(mine)
    } catch (e) {
      notifyError(t('dashboard.loadFailed'), e)
    }
  }

  useEffect(() => {
    let active = true
    Promise.all([api.listChannels(), api.listJoinedChannels()])
      .then(([owned, mine]) => {
        if (!active) return
        setChannels(owned)
        setJoined(mine)
      })
      .catch((e) => active && notifyError(t('dashboard.loadFailed'), e))
      .finally(() => active && setLoading(false))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function openCreate() {
    setName('')
    setMode('approval')
    setModalOpen(true)
  }

  function openJoin() {
    setJoinRef('')
    setJoinOpen(true)
  }

  useEffect(() => {
    let active = true
    getWebPushState()
      .then((s) => active && setPushOn(s.supported && s.permission === 'granted' && s.subscribed))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [])

  async function createChannel() {
    if (!name.trim()) return
    setCreating(true)
    try {
      const created = await api.createChannel(name.trim(), mode)
      setModalOpen(false)
      await refresh()
      notifySuccess(t('dashboard.created'))
      navigate(`/channels/${created.id}`)
    } catch (e) {
      notifyError(t('dashboard.createFailed'), e)
    } finally {
      setCreating(false)
    }
  }

  async function joinChannel() {
    const ref = joinRef.trim()
    if (!ref) return
    setJoining(true)
    try {
      const { channel } = await api.joinChannel(ref)
      setJoinOpen(false)
      await refresh()
      notifySuccess(t('dashboard.joined'))
      navigate(`/channels/${channel.id}`)
    } catch (e) {
      notifyError(t('dashboard.joinFailed'), e)
    } finally {
      setJoining(false)
    }
  }

  async function enableNotifications() {
    try {
      const deviceId = await registerWebPushDevice()
      saveWebDeviceId(deviceId)
      setPushOn(true)
      notifySuccess(t('dashboard.notificationsEnabled'))
    } catch (e) {
      notifyError(t('dashboard.enableFailed'), e)
    }
  }

  return (
    <Container size="sm">
      <Modal
        opened={modalOpen}
        onClose={() => setModalOpen(false)}
        title={t('dashboard.newChannel')}
        onEnterTransitionEnd={() => nameRef.current?.focus()}
      >
        <Stack gap="sm">
          <TextInput
            ref={nameRef}
            label={t('dashboard.channelName')}
            placeholder={t('dashboard.channelNamePlaceholder')}
            data-autofocus
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && createChannel()}
          />
          <div>
            <Text size="sm" fw={500} mb={4}>
              {t('dashboard.subscriptionMode')}
            </Text>
            <SegmentedControl
              fullWidth
              value={mode}
              onChange={(v) => setMode(v as SubscriptionMode)}
              data={[
                { label: t('mode.approval'), value: 'approval' },
                { label: t('mode.open'), value: 'open' },
              ]}
            />
          </div>
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={() => setModalOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={createChannel} loading={creating} disabled={!name.trim()}>
              {t('dashboard.createChannel')}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={joinOpen}
        onClose={() => setJoinOpen(false)}
        title={t('dashboard.addChannelTitle')}
        onEnterTransitionEnd={() => joinRefInput.current?.focus()}
      >
        <Stack gap="sm">
          <TextInput
            ref={joinRefInput}
            label={t('dashboard.addChannelRef')}
            placeholder={t('dashboard.addChannelRefPlaceholder')}
            description={t('dashboard.addChannelRefHint')}
            data-autofocus
            value={joinRef}
            onChange={(e) => setJoinRef(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && joinChannel()}
          />
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={() => setJoinOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={joinChannel} loading={joining} disabled={!joinRef.trim()}>
              {t('dashboard.add')}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Stack gap="xl">
        <Stack gap="md">
          <Group justify="space-between">
            <Title order={4}>{t('dashboard.yourChannels')}</Title>
            <Group gap="xs">
              {webPushSupported() &&
                (pushOn ? (
                  <Badge color="teal" variant="light" size="lg" leftSection={<IconBellCheck size={14} />}>
                    {t('dashboard.notificationsOn')}
                  </Badge>
                ) : (
                  <Button variant="subtle" size="xs" onClick={enableNotifications}>
                    {t('dashboard.enableNotifications')}
                  </Button>
                ))}
              <Button size="xs" variant="default" onClick={openJoin}>
                {t('dashboard.addChannel')}
              </Button>
              <Button size="xs" onClick={openCreate}>
                {t('dashboard.newChannel')}
              </Button>
            </Group>
          </Group>

          <Stack gap="sm">
            {loading && <CardListSkeleton rows={3} />}
            {!loading && channels.length === 0 && (
              <Text c="dimmed" size="sm">
                {t('dashboard.noChannels')}
              </Text>
            )}
            {!loading &&
              channels.map((c) => (
                <Card
                  key={c.id}
                  withBorder
                  padding="md"
                  className="pheme-card"
                  data-clickable="true"
                  style={{ cursor: 'pointer' }}
                  onClick={() => navigate(`/channels/${c.id}`)}
                >
                  <Group justify="space-between">
                    <Group gap="xs">
                      <Text fw={600}>{c.name}</Text>
                      {c.alias && (
                        <Text size="sm" c="dimmed">
                          @{c.alias}
                        </Text>
                      )}
                    </Group>
                    <ModeBadge mode={c.subscriptionMode} />
                  </Group>
                </Card>
              ))}
          </Stack>
        </Stack>

        {!loading && joined.length > 0 && (
          <Stack gap="md">
            <Title order={4}>{t('dashboard.joinedChannels')}</Title>
            <Stack gap="sm">
              {joined.map((c) => (
                <Card
                  key={c.id}
                  withBorder
                  padding="md"
                  className="pheme-card"
                  data-clickable="true"
                  style={{ cursor: 'pointer' }}
                  onClick={() => navigate(`/channels/${c.id}`)}
                >
                  <Group justify="space-between">
                    <Group gap="xs">
                      <Text fw={600}>{c.name}</Text>
                      {c.alias && (
                        <Text size="sm" c="dimmed">
                          @{c.alias}
                        </Text>
                      )}
                    </Group>
                    <Group gap="xs">
                      {c.role === 'admin' && <ChannelRoleBadge role={c.role} />}
                      <MemberStatusBadge status={c.memberStatus} />
                    </Group>
                  </Group>
                </Card>
              ))}
            </Stack>
          </Stack>
        )}
      </Stack>
    </Container>
  )
}
