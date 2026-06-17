import { AppShell, Button, Group, Text, Title } from '@mantine/core'
import { Outlet, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/context'

export function Layout() {
  const { logout } = useAuth()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  return (
    <AppShell header={{ height: 60 }} padding="md">
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="xs" style={{ cursor: 'pointer' }} onClick={() => navigate('/')}>
            <Title order={3}>Pheme</Title>
            <Text size="sm" c="dimmed">
              notification relay
            </Text>
          </Group>
          <Button variant="subtle" onClick={handleLogout}>
            Log out
          </Button>
        </Group>
      </AppShell.Header>
      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
    </AppShell>
  )
}
