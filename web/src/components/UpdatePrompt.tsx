import { Button, Group, Paper, Text } from '@mantine/core'
import { IconRefresh } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useAppOutdated } from '../hooks/useAppOutdated'

/**
 * Tells a tab that has fallen behind a deploy, and offers to reload it.
 *
 * It ASKS. Reloading someone's chat out from under them — mid-message, mid-call — to spare them a
 * stale tab is not a trade anyone would take, so the reload is a button and nothing here has a
 * timer on it.
 *
 * It also does not nag: the check latches (useAppOutdated), so this appears once and then either
 * gets used or sits there quietly. There is no dismiss, because the tab really is out of date and
 * saying so once is the whole job — a dismissed bar would just be a lie the next time it mattered.
 */
export function UpdatePrompt() {
  const { t } = useTranslation()
  const outdated = useAppOutdated()
  if (!outdated) return null

  return (
    <Paper className="pheme-update-prompt" role="status" shadow="md" radius="md" p="xs">
      <Group gap="sm" wrap="nowrap">
        <Text size="sm">{t('update.available')}</Text>
        <Button
          size="compact-sm"
          variant="light"
          color="iris"
          leftSection={<IconRefresh size={14} />}
          onClick={() => window.location.reload()}
        >
          {t('update.reload')}
        </Button>
      </Group>
    </Paper>
  )
}
