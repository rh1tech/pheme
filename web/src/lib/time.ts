// Date formatting for the chat surface. All functions take the active locale and
// a translator, so they stay pure and testable rather than reaching into i18next.

type Translate = (key: string) => string

const DAY_MS = 24 * 60 * 60 * 1000

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
}

/** Whole days between two instants, counted by calendar day, not elapsed hours. */
function daysApart(a: Date, b: Date): number {
  return Math.round((startOfDay(b) - startOfDay(a)) / DAY_MS)
}

/** True when both instants fall on the same calendar day. */
export function isSameDay(a: string, b: string): boolean {
  return startOfDay(new Date(a)) === startOfDay(new Date(b))
}

/** Clock time, e.g. "18:11". Used on message bubbles. */
export function messageTime(iso: string, locale: string): string {
  return new Date(iso).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' })
}

/**
 * The timestamp on a chat-list row, Telegram-style: clock time today, "Yesterday"
 * yesterday, the weekday within the last week, and a short date beyond that.
 */
export function chatListTime(iso: string, locale: string, t: Translate): string {
  const then = new Date(iso)
  const days = daysApart(then, new Date())
  if (days <= 0) return messageTime(iso, locale)
  if (days === 1) return t('chat.yesterday')
  if (days < 7) return then.toLocaleDateString(locale, { weekday: 'short' })
  return then.toLocaleDateString(locale, { day: '2-digit', month: '2-digit', year: '2-digit' })
}

/** The label on a date separator in the feed: "Today", "Yesterday", or a date. */
export function dayLabel(iso: string, locale: string, t: Translate): string {
  const then = new Date(iso)
  const days = daysApart(then, new Date())
  if (days <= 0) return t('chat.today')
  if (days === 1) return t('chat.yesterday')
  const sameYear = then.getFullYear() === new Date().getFullYear()
  return then.toLocaleDateString(locale, {
    day: 'numeric',
    month: 'long',
    year: sameYear ? undefined : 'numeric',
  })
}
