import { Menu, UnstyledButton, Group, Text } from '@mantine/core'
import { IconLanguage, IconCheck } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { SUPPORTED_LANGUAGES, type Language } from '../i18n'

const LABELS: Record<Language, string> = {
  en: 'English',
  ru: 'Русский',
}

// Real-time language switcher. Changing the language calls i18next, which
// re-renders all translated components immediately and persists the choice.
export function LanguageSwitcher() {
  const { i18n } = useTranslation()
  const current = (i18n.resolvedLanguage ?? 'en') as Language

  return (
    <Menu position="bottom-end" withinPortal>
      <Menu.Target>
        <UnstyledButton aria-label="Change language">
          <Group gap={6}>
            <IconLanguage size={18} />
            <Text size="sm" tt="uppercase">
              {current}
            </Text>
          </Group>
        </UnstyledButton>
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
