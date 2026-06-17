import { useEffect, useState } from 'react'
import {
  Badge,
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
import { notifications } from '@mantine/notifications'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import type { Channel, SubscriptionMode } from '../lib/types'
import { registerWebPushDevice, webPushSupported } from '../lib/webpush'
import { saveWebDeviceId } from '../lib/device'

export function DashboardPage() {
  const navigate = useNavigate()
  const [channels, setChannels] = useState<Channel[]>([])
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
    // Initial data fetch on mount; state is set after the awaited response.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    refresh()
  }, [])

  async function createChannel() {
    if (!name.trim()) return
    setCreating(true)
    try {
      await api.createChannel(name.trim(), mode)
      setName('')
      await refresh()
      notifications.show({ color: 'green', message: 'Channel created' })
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
      <Stack gap="lg">
        <Group justify="space-between">
          <Title order={4}>Your channels</Title>
          {webPushSupported() && (
            <Button variant="light" size="xs" onClick={enableNotifications}>
              Enable browser notifications
            </Button>
          )}
        </Group>

        <Card withBorder padding="lg">
          <Stack gap="sm">
            <TextInput
              label="New channel name"
              placeholder="Site Alerts"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
            />
            <SegmentedControl
              value={mode}
              onChange={(v) => setMode(v as SubscriptionMode)}
              data={[
                { label: 'Approval', value: 'approval' },
                { label: 'Open', value: 'open' },
              ]}
            />
            <Group justify="flex-end">
              <Button onClick={createChannel} loading={creating}>
                Create channel
              </Button>
            </Group>
          </Stack>
        </Card>

        <Stack gap="sm">
          {channels.length === 0 && (
            <Text c="dimmed" size="sm">
              No channels yet — create one above.
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
