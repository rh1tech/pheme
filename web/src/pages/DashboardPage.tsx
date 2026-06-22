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
import { notifications } from '@mantine/notifications'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Channel, SubscriptionMode } from '../lib/types'
import { registerWebPushDevice, webPushSupported } from '../lib/webpush'
import { saveWebDeviceId } from '../lib/device'

export function DashboardPage() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [channels, setChannels] = useState<Channel[]>([])
  const [modalOpen, setModalOpen] = useState(false)
  const [name, setName] = useState('')
  const [mode, setMode] = useState<SubscriptionMode>('approval')
  const [creating, setCreating] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)

  async function refresh() {
    try {
      setChannels(await api.listChannels())
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('dashboard.loadFailed')}: ${String(e)}` })
    }
  }

  useEffect(() => {
    let active = true
    api
      .listChannels()
      .then((cs) => active && setChannels(cs))
      .catch(
        (e) =>
          active &&
          notifications.show({ color: 'red', message: `${t('dashboard.loadFailed')}: ${String(e)}` }),
      )
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

  async function createChannel() {
    if (!name.trim()) return
    setCreating(true)
    try {
      const created = await api.createChannel(name.trim(), mode)
      setModalOpen(false)
      await refresh()
      notifications.show({ color: 'green', message: t('dashboard.created') })
      navigate(`/channels/${created.id}`)
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('dashboard.createFailed')}: ${String(e)}` })
    } finally {
      setCreating(false)
    }
  }

  async function enableNotifications() {
    try {
      const deviceId = await registerWebPushDevice()
      saveWebDeviceId(deviceId)
      notifications.show({ color: 'green', message: t('dashboard.notificationsEnabled') })
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('dashboard.enableFailed')}: ${(e as Error).message}` })
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

      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>{t('dashboard.yourChannels')}</Title>
          <Group gap="xs">
            {webPushSupported() && (
              <Button variant="subtle" size="xs" onClick={enableNotifications}>
                {t('dashboard.enableNotifications')}
              </Button>
            )}
            <Button size="xs" onClick={openCreate}>
              {t('dashboard.newChannel')}
            </Button>
          </Group>
        </Group>

        <Stack gap="sm">
          {channels.length === 0 && (
            <Text c="dimmed" size="sm">
              {t('dashboard.noChannels')}
            </Text>
          )}
          {channels.map((c) => (
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
                <Text fw={600}>{c.name}</Text>
                <Badge color={c.subscriptionMode === 'open' ? 'teal' : 'grape'}>
                  {t(`mode.${c.subscriptionMode}`)}
                </Badge>
              </Group>
            </Card>
          ))}
        </Stack>
      </Stack>
    </Container>
  )
}
