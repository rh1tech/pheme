/*
 * Derived from Telegram Web K (GPL v3):
 *   https://github.com/morethanwords/tweb — src/components/chat/bubbles.ts
 *   (the `is-first-unread` marker / `attachedUnreadBubble`)
 * See web/NOTICE.md.
 */
import { useTranslation } from 'react-i18next'

/**
 * The line a channel opens on when it has unread messages — everything below it
 * is new since the reader was last here.
 */
export function UnreadDivider() {
  const { t } = useTranslation()
  return (
    <div className="pheme-unread-divider" data-testid="unread-divider">
      <span>{t('chat.unreadMessages')}</span>
    </div>
  )
}
