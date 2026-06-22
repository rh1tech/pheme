import { ActionIcon, Tooltip, useMantineColorScheme, useComputedColorScheme } from '@mantine/core'
import { IconMoon, IconSun } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'

// Toggles between light and dark color schemes. Mantine persists the choice to
// localStorage via its default color-scheme manager, so it survives reloads.
export function ThemeToggle() {
  const { setColorScheme } = useMantineColorScheme()
  const computed = useComputedColorScheme('light', { getInitialValueInEffect: true })
  const { t } = useTranslation()

  return (
    <Tooltip label={t('common.toggleTheme')} withArrow>
      <ActionIcon
        variant="subtle"
        color="gray"
        size="lg"
        aria-label={t('common.toggleTheme')}
        onClick={() => setColorScheme(computed === 'dark' ? 'light' : 'dark')}
      >
        {computed === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
      </ActionIcon>
    </Tooltip>
  )
}
