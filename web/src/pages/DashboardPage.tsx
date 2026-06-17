import { useEffect, useState } from 'react'
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
import { api } from '../lib/api'
import type { Channel, SubscriptionMode } from '../lib/types'
import { registerWebPushDevice, webPushSupported } from '../lib/webpush'
import { saveWebDeviceId } from '../lib/device'

export function DashboardPage() {
  const navigate = useNavigate()
  const [channels, setChannels] = useState<Channel[]>([])
  const [modalOpen, setModalOpen] = useState(false)
  const [name, setName] = useState('')
  const [mode, setMode] = useState<SubscriptionMode>('approval')
  const [creating, setCreating] = useState(false)

  async function refresh() {
    try {
      setChannels(await api.listChannels())
    } catch (e) {
      notifications.show({ color: 'red', message: `Load failed: ${String(e)}` })
    }
  }

  useEffect(() => {
    let active = true
    api
      .listChannels()
      .then((cs) => active && setChannels(cs))
      .catch((e) => active && notifications.show({ color: 'red', message: `Load failed: ${String(e)}` }))
    return () => {
      active = false
    }
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
      notifications.show({ color: 'green', message: 'Channel created' })
      navigate(`/channels/${created.id}`)
    } catch (e) {
      notifications.show({ color: 'red', message: `Create failed: ${String(e)}` })
    } finally {
      setCreating(false)
    }
  }

  async function enableNotifications() {
    try {
      const deviceId = await registerWebPushDevice()
      saveWebDeviceId(deviceId)
      notifications.show({ color: 'green', message: 'Browser notifications enabled' })
    } catch (e) {
      notifications.show({ color: 'red', message: `Could not enable: ${(e as Error).message}` })
    }
  }

  return (
    <Container size="sm">
      <Modal opened={modalOpen} onClose={() => setModalOpen(false)} title="New channel">
        <Stack gap="sm">
          <TextInput
            label="Channel name"
            placeholder="Site Alerts"
            data-autofocus
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && createChannel()}
          />
          <div>
            <Text size="sm" fw={500} mb={4}>
              Subscription mode
            </Text>
            <SegmentedControl
              fullWidth
              value={mode}
              onChange={(v) => setMode(v as SubscriptionMode)}
              data={[
                { label: 'Approval', value: 'approval' },
                { label: 'Open', value: 'open' },
              ]}
            />
          </div>
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={() => setModalOpen(false)}>
              Cancel
            </Button>
            <Button onClick={createChannel} loading={creating} disabled={!name.trim()}>
              Create channel
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Stack gap="lg">
        <Group justify="space-between">
          <Title order={4}>Your channels</Title>
          <Group gap="xs">
            {webPushSupported() && (
              <Button variant="subtle" size="xs" onClick={enableNotifications}>
                Enable notifications
              </Button>
            )}
            <Button size="xs" onClick={openCreate}>
              New channel
            </Button>
          </Group>
        </Group>

        <Stack gap="sm">
          {channels.length === 0 && (
            <Text c="dimmed" size="sm">
              No channels yet — create one with “New channel”.
            </Text>
          )}
          {channels.map((c) => (
            <Card
              key={c.id}
              withBorder
              padding="md"
              style={{ cursor: 'pointer' }}
              onClick={() => navigate(`/channels/${c.id}`)}
            >
              <Group justify="space-between">
                <Text fw={600}>{c.name}</Text>
                <Badge color={c.subscriptionMode === 'open' ? 'teal' : 'grape'}>
                  {c.subscriptionMode}
                </Badge>
              </Group>
            </Card>
          ))}
        </Stack>
      </Stack>
    </Container>
  )
}
