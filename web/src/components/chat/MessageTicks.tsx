import { IconCheck, IconChecks } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import type { Receipt } from '../../lib/receipts'

interface MessageTicksProps {
  receipt: Receipt
}

/**
 * The ticks on your own message: one when it has reached everyone, two when everyone has read it.
 *
 * Nothing at all until it has been delivered. A message that has left is simply there — the ticks
 * are news, and "no news yet" is better said with silence than with a third symbol nobody can
 * remember the meaning of.
 *
 * Only ever drawn on your own messages: ticking someone else's would be telling them what they
 * already know.
 */
export function MessageTicks({ receipt }: MessageTicksProps) {
  const { t } = useTranslation()
  if (receipt === 'sent') return null

  const read = receipt === 'read'
  const Icon = read ? IconChecks : IconCheck
  return (
    <Icon
      size={14}
      stroke={2.2}
      className="pheme-ticks"
      data-read={read}
      // A tick is meaningless to a screen reader, so it carries the sentence instead.
      role="img"
      aria-label={read ? t('chat.receiptRead') : t('chat.receiptDelivered')}
    />
  )
}
