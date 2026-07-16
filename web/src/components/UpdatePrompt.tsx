import { useState } from 'react'
import { ActionIcon, Button, Group, Paper, Text } from '@mantine/core'
import { IconRefresh, IconX } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useAppOutdated } from '../hooks/useAppOutdated'

/**
 * Tells a tab that has fallen behind a deploy, and offers to reload it.
 *
 * It ASKS. Reloading someone's chat out from under them — mid-message, mid-call — to spare them a
 * stale tab is not a trade anyone would take, so the reload is a button and nothing here has a
 * timer on it.
 *
 * It can also be dismissed, because it sits over the app until it is acted on and there is no
 * bottom edge that is free: the chat list has its tabs there, an open conversation has its
 * composer. Being permanently unable to close a bar that is only telling you something is a worse
 * bug than the staleness it reports. Dismissing latches for the life of the tab rather than
 * snoozing — the next poll would only say the same thing, and asking twice is nagging.
 */
export function UpdatePrompt() {
  const { t } = useTranslation()
  const outdated = useAppOutdated()
  const [dismissed, setDismissed] = useState(false)
  if (!outdated || dismissed) return null

  return (
    <Paper className="pheme-update-prompt" role="status" shadow="md" radius="md" p="xs">
      <Group gap="sm" wrap="nowrap">
        <Text size="sm">{t('update.available')}</Text>
        {/* The label must never be the thing that gives way: a Group child shrinks by default, and
            at phone width that clipped "Reload" to "R". */}
        <Button
          size="compact-sm"
          variant="light"
          color="iris"
          leftSection={<IconRefresh size={14} />}
          onClick={() => window.location.reload()}
          style={{ flexShrink: 0 }}
        >
          {t('update.reload')}
        </Button>
        <ActionIcon
          variant="subtle"
          color="gray"
          aria-label={t('update.dismiss')}
          onClick={() => setDismissed(true)}
          style={{ flexShrink: 0 }}
        >
          <IconX size={16} />
        </ActionIcon>
      </Group>
    </Paper>
  )
}
