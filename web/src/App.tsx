import { useEffect, useState } from 'react'
import {
  AppShell,
  Button,
  Card,
  Container,
  Group,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  Title,
  Badge,
  Code,
  CopyButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api, type Channel } from './api'

function App() {
  const [channels, setChannels] = useState<Channel[]>([])
  const [name, setName] = useState('')
  const [mode, setMode] = useState<'open' | 'approval'>('approval')
  const [loading, setLoading] = useState(false)

  async function refresh() {
    try {
      setChannels(await api.listChannels())
    } catch (e) {
      notifications.show({ color: 'red', message: `Load failed: ${String(e)}` })
    }
  }

  useEffect(() => {
    refresh()
  }, [])

  async function create() {
    if (!name.trim()) return
    setLoading(true)
    try {
      await api.createChannel(name.trim(), mode)
      setName('')
      await refresh()
      notifications.show({ color: 'green', message: 'Channel created' })
    } catch (e) {
      notifications.show({ color: 'red', message: `Create failed: ${String(e)}` })
    } finally {
      setLoading(false)
    }
  }

  async function makeKey(channelId: string) {
    try {
      const k = await api.createKey(channelId)
      notifications.show({
        color: 'blue',
        autoClose: false,
        title: 'API key (shown once)',
        message: k.key,
      })
    } catch (e) {
      notifications.show({ color: 'red', message: `Key failed: ${String(e)}` })
    }
  }

  return (
    <AppShell header={{ height: 60 }} padding="md">
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Title order={3}>Pheme</Title>
          <Text size="sm" c="dimmed">
            notification relay
          </Text>
        </Group>
      </AppShell.Header>

      <AppShell.Main>
        <Container size="sm">
          <Stack gap="lg">
            <Card withBorder padding="lg">
              <Title order={5} mb="sm">
                New channel
              </Title>
              <Stack gap="sm">
                <TextInput
                  label="Name"
                  placeholder="Site Alerts"
                  value={name}
                  onChange={(e) => setName(e.currentTarget.value)}
                />
                <SegmentedControl
                  value={mode}
                  onChange={(v) => setMode(v as 'open' | 'approval')}
                  data={[
                    { label: 'Approval', value: 'approval' },
                    { label: 'Open', value: 'open' },
                  ]}
                />
                <Group justify="flex-end">
                  <Button onClick={create} loading={loading}>
                    Create
                  </Button>
                </Group>
              </Stack>
            </Card>

            <Stack gap="sm">
              <Title order={5}>Channels</Title>
              {channels.length === 0 && (
                <Text c="dimmed" size="sm">
                  No channels yet.
                </Text>
              )}
              {channels.map((c) => (
                <Card key={c.id} withBorder padding="md">
                  <Group justify="space-between">
                    <Stack gap={2}>
                      <Text fw={600}>{c.name}</Text>
                      <Group gap="xs">
                        <Code>{c.publicId}</Code>
                        <CopyButton value={c.publicId}>
                          {({ copied, copy }) => (
                            <Button size="compact-xs" variant="subtle" onClick={copy}>
                              {copied ? 'Copied' : 'Copy ID'}
                            </Button>
                          )}
                        </CopyButton>
                      </Group>
                    </Stack>
                    <Group>
                      <Badge color={c.subscriptionMode === 'open' ? 'teal' : 'grape'}>
                        {c.subscriptionMode}
                      </Badge>
                      <Button size="xs" variant="light" onClick={() => makeKey(c.id)}>
                        New key
                      </Button>
                    </Group>
                  </Group>
                </Card>
              ))}
            </Stack>
          </Stack>
        </Container>
      </AppShell.Main>
    </AppShell>
  )
}

export default App
