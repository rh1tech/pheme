import { Menu, Tooltip, UnstyledButton, Group, Text } from '@mantine/core'
import { IconLanguage, IconCheck } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { SUPPORTED_LANGUAGES, type Language } from '../i18n'

const LABELS: Record<Language, string> = {
  en: 'English',
  ru: 'Русский',
}

// Real-time language switcher. Changing the language calls i18next, which
// re-renders all translated components immediately and persists the choice.
// Styled to match the adjacent icon buttons, but keeps the current language
// code visible.
export function LanguageSwitcher() {
  const { t, i18n } = useTranslation()
  const current = (i18n.resolvedLanguage ?? 'en') as Language

  return (
    <Menu position="top-end" withArrow withinPortal>
      <Menu.Target>
        <Tooltip label={t('language.label')} withArrow>
          <UnstyledButton
            aria-label={t('language.label')}
            style={{
              height: 'var(--mantine-spacing-xl)',
              padding: '0 8px',
              borderRadius: 'var(--mantine-radius-md)',
              display: 'flex',
              alignItems: 'center',
            }}
          >
            <Group gap={6} c="gray">
              <IconLanguage size={18} />
              <Text size="sm" fw={500} tt="uppercase">
                {current}
              </Text>
            </Group>
          </UnstyledButton>
        </Tooltip>
      </Menu.Target>
      <Menu.Dropdown>
        {SUPPORTED_LANGUAGES.map((lng) => (
          <Menu.Item
            key={lng}
            onClick={() => i18n.changeLanguage(lng)}
            rightSection={current === lng ? <IconCheck size={14} /> : null}
          >
            {LABELS[lng]}
          </Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  )
}
