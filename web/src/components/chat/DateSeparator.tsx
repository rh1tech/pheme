import { useTranslation } from 'react-i18next'
import { dayLabel } from '../../lib/time'

interface DateSeparatorProps {
  /** Any message timestamp from the day being separated. */
  iso: string
}

export function DateSeparator({ iso }: DateSeparatorProps) {
  const { t, i18n } = useTranslation()
  return <div className="pheme-day-pill">{dayLabel(iso, i18n.language, t)}</div>
}
