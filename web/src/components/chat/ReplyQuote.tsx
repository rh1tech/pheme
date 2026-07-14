// The quoted message above a reply.
//
// The quote is rendered from the message THIS DEVICE ALREADY HOLDS, never from text copied into the
// reply. That is a security property, not a storage optimisation: if the sender could supply the
// quoted text, they could quote you as having said anything at all, and the recipient would have no
// way to check. So a reply carries an id and nothing else, and each device looks the original up for
// itself.
//
// Which means a device sometimes cannot show the quote — the quoted message predates its joining the
// group, so it can never decrypt it. It says so, rather than showing a plausible-looking blank.

import { Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'

interface ReplyQuoteProps {
  /** Who wrote the quoted message. Undefined when we cannot read it. */
  author?: string
  /** The quoted text, or undefined when this device cannot read the original. */
  text?: string
  onClick?: () => void
  /** The composer variant, which is a shade tighter. */
  compact?: boolean
}

export function ReplyQuote({ author, text, onClick, compact = false }: ReplyQuoteProps) {
  const { t } = useTranslation()
  const unreadable = text === undefined

  return (
    <div
      className="pheme-reply-quote"
      data-compact={compact}
      data-clickable={Boolean(onClick)}
      onClick={onClick}
    >
      <Text size="xs" fw={600} c="iris" truncate>
        {author ?? t('chat.replyUnknown')}
      </Text>
      <Text size="xs" c="dimmed" fs={unreadable ? 'italic' : undefined} truncate>
        {/* Not an ellipsis, and not a blank: this device will NEVER be able to read that message, and
            implying it is still loading would be a lie it never resolves. */}
        {text ?? t('chat.replyUnavailable')}
      </Text>
    </div>
  )
}
