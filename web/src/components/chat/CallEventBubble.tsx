import { Text } from '@mantine/core'
import { IconPhoneOff, IconPhoneX } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import type { CallEvent } from '../../lib/callEvent'

interface CallEventBubbleProps {
  event: CallEvent
  /** True when we are the one who placed the call that went unanswered. */
  own: boolean
  /** Clock time, already formatted for the active locale. */
  at: string
}

/**
 * The trace a call leaves when nobody picks up.
 *
 * Deliberately not styled as a chat bubble. It is not something anybody said, and dressing it up
 * as speech would put words in the caller's mouth — it is a note about what happened, and it
 * reads as one: centred, quiet, and unmistakably not a message.
 *
 * The same event says different things to the two people. The person who called was not missed
 * by anybody; they simply got no answer.
 */
export function CallEventBubble({ event, own, at }: CallEventBubbleProps) {
  const { t } = useTranslation()

  const label =
    event.outcome === 'declined'
      ? t(own ? 'call.eventDeclinedOut' : 'call.eventDeclinedIn')
      : event.outcome === 'failed'
        ? t('call.eventFailed')
        : t(own ? 'call.eventMissedOut' : 'call.eventMissedIn')

  const Icon = event.outcome === 'declined' ? IconPhoneX : IconPhoneOff

  return (
    <div className="pheme-call-event" data-testid="call-event" data-outcome={event.outcome}>
      <Icon size={15} stroke={1.75} aria-hidden />
      <Text span size="xs" fw={500}>
        {label}
      </Text>
      <Text span size="xs" c="dimmed">
        {at}
      </Text>
    </div>
  )
}
