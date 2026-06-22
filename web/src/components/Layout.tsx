import { ActionIcon, AppShell, Burger, Group, NavLink, ScrollArea, Stack, Tooltip } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { IconBroadcast, IconShieldCog, IconLogout, IconUsers } from '@tabler/icons-react'
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
  const [opened, { toggle, close }] = useDisclosure(false)

  function go(path: string) {
    navigate(path)
    close()
  }

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  const onChannels = location.pathname === '/' || location.pathname.startsWith('/channels')
  const isOverview = location.pathname === '/admin'
  const onAdminUsers = location.pathname.startsWith('/admin/users')
  const onAdminChannels = location.pathname.startsWith('/admin/channels')

  return (
    <AppShell
      header={{ height: { base: 56, sm: 0 } }}
      navbar={{ width: 248, breakpoint: 'sm', collapsed: { mobile: !opened } }}
      padding="lg"
    >
      <AppShell.Header
        hiddenFrom="sm"
        style={{
          backdropFilter: 'blur(8px)',
          backgroundColor: 'light-dark(rgba(255,255,255,0.8), rgba(20,21,23,0.8))',
        }}
      >
        <Group h="100%" px="md" justify="space-between">
          <Burger opened={opened} onClick={toggle} size="sm" aria-label={t('common.menu')} />
          <Logo onClick={() => go('/')} />
          <ThemeToggle />
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="md">
        <AppShell.Section>
          <Group justify="space-between" mb="lg" mt={4}>
            <Logo onClick={() => go('/')} />
          </Group>
        </AppShell.Section>

        <AppShell.Section grow component={ScrollArea}>
          <Stack gap={4}>
            <NavLink
              active={onChannels}
              label={t('common.navChannels')}
              leftSection={<IconBroadcast size={18} />}
              onClick={() => go('/')}
              variant="filled"
            />
            {isAdmin && (
              <NavLink
                active={isOverview}
                label={t('admin.nav')}
                leftSection={<IconShieldCog size={18} />}
                onClick={() => go('/admin')}
                opened
                childrenOffset={28}
                variant="filled"
              >
                <NavLink
                  active={onAdminUsers}
                  label={t('admin.navUsers')}
                  leftSection={<IconUsers size={18} />}
                  onClick={() => go('/admin/users')}
                  variant="filled"
                />
                <NavLink
                  active={onAdminChannels}
                  label={t('admin.navChannels')}
                  leftSection={<IconBroadcast size={18} />}
                  onClick={() => go('/admin/channels')}
                  variant="filled"
                />
              </NavLink>
            )}
          </Stack>
        </AppShell.Section>

        <AppShell.Section>
          <Group gap="xs" justify="flex-start" mt="md">
            <Tooltip label={t('common.logout')} withArrow>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="lg"
                aria-label={t('common.logout')}
                onClick={handleLogout}
              >
                <IconLogout size={18} />
              </ActionIcon>
            </Tooltip>
            <ThemeToggle />
            <LanguageSwitcher />
          </Group>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
    </AppShell>
  )
}
