import { ActionIcon, Menu, useComputedColorScheme, useMantineColorScheme } from '@mantine/core'
import {
  IconCheck,
  IconLanguage,
  IconLogout,
  IconMenu2,
  IconMoon,
  IconShieldCog,
  IconSun,
  IconUserCircle,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../auth/context'
import { SUPPORTED_LANGUAGES, type Language } from '../../i18n'

/**
 * The burger in the chat-list header. Everything the old left nav held that is
 * not a channel lives here: profile, admin, appearance, language and the session.
 *
 * Theme and language are menu items in their own right rather than a row of icon
 * buttons squeezed into the dropdown: a menu is a list of choices, and two bare
 * glyphs under a heading read as decoration, not as things to click.
 */
export function ChatSidebarMenu() {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const { logout, isAdmin } = useAuth()
  const { setColorScheme } = useMantineColorScheme()
  const scheme = useComputedColorScheme('light', { getInitialValueInEffect: true })
  const isDark = scheme === 'dark'
  const language = (i18n.resolvedLanguage ?? 'en') as Language

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  return (
    <Menu position="bottom-start" width={240} shadow="md">
      <Menu.Target>
        <ActionIcon variant="subtle" color="gray" size="lg" aria-label={t('chat.menu')}>
          <IconMenu2 size={20} />
        </ActionIcon>
      </Menu.Target>

      <Menu.Dropdown>
        <Menu.Item leftSection={<IconUserCircle size={18} />} onClick={() => navigate('/profile')}>
          {t('common.navProfile')}
        </Menu.Item>
        {isAdmin && (
          <Menu.Item leftSection={<IconShieldCog size={18} />} onClick={() => navigate('/admin')}>
            {t('admin.nav')}
          </Menu.Item>
        )}

        <Menu.Divider />

        {/* The label names the scheme it switches TO, which is what the reader is
            choosing — not the one they are already looking at. */}
        <Menu.Item
          leftSection={isDark ? <IconSun size={18} /> : <IconMoon size={18} />}
          onClick={() => setColorScheme(isDark ? 'light' : 'dark')}
        >
          {isDark ? t('common.lightMode') : t('common.darkMode')}
        </Menu.Item>

        <Menu.Sub>
          <Menu.Sub.Target>
            <Menu.Sub.Item leftSection={<IconLanguage size={18} />}>
              {t('language.label')}
            </Menu.Sub.Item>
          </Menu.Sub.Target>
          <Menu.Sub.Dropdown>
            {SUPPORTED_LANGUAGES.map((lng) => (
              <Menu.Item
                key={lng}
                onClick={() => i18n.changeLanguage(lng)}
                rightSection={language === lng ? <IconCheck size={14} /> : null}
              >
                {t(`language.${lng}`)}
              </Menu.Item>
            ))}
          </Menu.Sub.Dropdown>
        </Menu.Sub>

        <Menu.Divider />

        <Menu.Item color="red" leftSection={<IconLogout size={18} />} onClick={handleLogout}>
          {t('common.logout')}
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  )
}
