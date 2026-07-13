import { Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { Logo } from '../../components/Logo'

/**
 * The conversation column at `/` — before a channel is picked. On a phone the
 * conversation column is hidden entirely, so this is a desktop-only sight.
 */
export function ChatEmptyState() {
  const { t } = useTranslation()
  return (
    <section className="pheme-conversation">
      <div className="pheme-empty">
        <Stack align="center" gap="xs">
          <Logo />
          <Text c="dimmed" size="sm">
            {t('chat.pickChannel')}
          </Text>
          <Text c="dimmed" size="xs">
            {t('chat.pickChannelHint')}
          </Text>
        </Stack>
      </div>
    </section>
  )
}
