import { useEffect, useRef, useState } from 'react'
import {
  Button,
  Card,
  Container,
  Group,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { notifyError, notifySuccess } from '../lib/notify'
import type { Channel, JoinedChannel, SubscriptionMode } from '../lib/types'
import { ChannelRoleBadge, MemberStatusBadge, ModeBadge } from '../components/badges'
import { CardListSkeleton } from '../components/Skeletons'
import { PullToRefresh } from '../components/PullToRefresh'
import { ResponsiveModal } from '../components/ResponsiveModal'

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

  return (
    <Container size="sm">
      <ResponsiveModal
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
      </ResponsiveModal>

      <ResponsiveModal
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
      </ResponsiveModal>

      <PullToRefresh onRefresh={refresh}>
      <Stack gap="xl">
        <Stack gap="md">
          <Group justify="space-between">
            <Title order={4}>{t('dashboard.yourChannels')}</Title>
            <Group gap="xs">
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
      </PullToRefresh>
    </Container>
  )
}
