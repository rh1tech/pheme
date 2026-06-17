import { AppShell, Button, Group } from '@mantine/core'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../auth/context'
import { LanguageSwitcher } from './LanguageSwitcher'
import { ThemeToggle } from './ThemeToggle'
import { Logo } from './Logo'

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
    <AppShell header={{ height: 64 }} padding="lg">
      <AppShell.Header
        withBorder
        style={{
          backdropFilter: 'blur(8px)',
          backgroundColor: 'light-dark(rgba(255,255,255,0.8), rgba(20,21,23,0.8))',
        }}
      >
        <Group h="100%" px="lg" justify="space-between">
          <Group gap="lg">
            <Logo onClick={() => navigate('/')} />
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
          <Group gap="sm">
            <ThemeToggle />
            <LanguageSwitcher />
            <Button variant="default" size="xs" onClick={handleLogout}>
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
