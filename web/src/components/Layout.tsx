import { AppShell, Button, Group, Title } from '@mantine/core'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../auth/context'
import { LanguageSwitcher } from './LanguageSwitcher'
import { ThemeToggle } from './ThemeToggle'

export function Layout() {
  const { logout, isAdmin } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation()

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  const onAdmin = location.pathname.startsWith('/admin')

  return (
    <AppShell header={{ height: 60 }} padding="md">
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="md">
            <Title order={3} style={{ cursor: 'pointer' }} onClick={() => navigate('/')}>
              {t('common.appName')}
            </Title>
            {isAdmin && (
              <Button
                variant={onAdmin ? 'light' : 'subtle'}
                size="xs"
                onClick={() => navigate('/admin')}
              >
                {t('admin.nav')}
              </Button>
            )}
          </Group>
          <Group gap="md">
            <ThemeToggle />
            <LanguageSwitcher />
            <Button variant="subtle" onClick={handleLogout}>
              {t('common.logout')}
            </Button>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
    </AppShell>
  )
}
