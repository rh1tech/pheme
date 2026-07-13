import { Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'

/** Stands in for the composer for members who cannot post — Pheme channels are broadcast-only. */
export function ReadOnlyNotice() {
  const { t } = useTranslation()
  return (
    <div className="pheme-readonly">
      <Text size="sm" c="dimmed">
        {t('chat.readOnly')}
      </Text>
    </div>
  )
}
