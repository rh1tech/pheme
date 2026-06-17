import { AppShell, Button, Group, Title } from '@mantine/core'
import { Outlet, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../auth/context'
import { LanguageSwitcher } from './LanguageSwitcher'
import { ThemeToggle } from './ThemeToggle'

export function Layout() {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const { t } = useTranslation()

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  return (
    <AppShell header={{ height: 60 }} padding="md">
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="xs" style={{ cursor: 'pointer' }} onClick={() => navigate('/')}>
            <Title order={3}>{t('common.appName')}</Title>
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
