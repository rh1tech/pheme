import { useEffect, useRef } from 'react'
import { Center, Loader, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { MessageBubble } from './MessageBubble'
import { DateSeparator } from './DateSeparator'
import { UnreadDivider } from './UnreadDivider'
import { JumpToBottom } from './JumpToBottom'
import { ChatSkeleton } from './ChatSkeleton'
import { isSameDay } from '../../lib/time'
import type { Message } from '../../lib/types'
import type { RefObject } from 'react'

// The scroll refs are taken apart from the reactive scroll state rather than
// passed as one object: bundling a ref with values that drive rendering makes the
// whole object read as a ref, and reading it during render is a bug the compiler
// (rightly) rejects.
interface MessageFeedProps {
  /** Oldest-first — the order they are rendered and read in. */
  messages: Message[]
  loading: boolean
  loadingOlder: boolean
  /** True while older pages remain; drives the top sentinel. */
  hasOlder: boolean
  onLoadOlder: () => void
  scrollerRef: RefObject<HTMLDivElement | null>
  contentRef: RefObject<HTMLDivElement | null>
  atBottom: boolean
  onJumpToBottom: () => void
  /** Messages that arrived while the reader was scrolled up. */
  unseenCount: number
  activeMessageId?: string
  /** The message the unread divider sits above; undefined when nothing is unread. */
  firstUnreadId?: string
  onOpenDiscussion: (messageId: string) => void
  /** Shown instead of the generic empty text when a search returned nothing. */
  searching: boolean
}

export function MessageFeed({
  messages,
  loading,
  loadingOlder,
  hasOlder,
  onLoadOlder,
  scrollerRef,
  contentRef,
  atBottom,
  onJumpToBottom,
  unseenCount,
  activeMessageId,
  firstUnreadId,
  onOpenDiscussion,
  searching,
}: MessageFeedProps) {
  const { t } = useTranslation()
  const sentinelRef = useRef<HTMLDivElement>(null)
  // Held in a ref so the observer is not torn down and rebuilt on every render.
  const loadOlder = useRef(onLoadOlder)
  useEffect(() => {
    loadOlder.current = onLoadOlder
  })

  // Older pages load when the top of the list comes into view. An
  // IntersectionObserver keeps this off the scroll event path, so a fast flick
  // upward does not queue a burst of handlers.
  useEffect(() => {
    const el = sentinelRef.current
    const root = scrollerRef.current
    if (!el || !root || !hasOlder) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) loadOlder.current()
      },
      { root, rootMargin: '300px 0px 0px 0px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [hasOlder, scrollerRef])

  return (
    <div className="pheme-feed-wrap">
      <div className="pheme-feed" ref={scrollerRef} data-testid="chat-feed">
        {/* The messages live in their own element so its height can be observed:
            that is what keeps the feed glued to the newest message as pages land
            and images decode. See useChatScroll. */}
        <div className="pheme-feed-content" ref={contentRef}>
          {hasOlder && <div ref={sentinelRef} aria-hidden />}

          {loadingOlder && (
            <Center py="xs">
              <Loader size="xs" aria-label={t('channel.loadingOlder')} />
            </Center>
          )}

          {loading && <ChatSkeleton />}

          {!loading && messages.length === 0 && (
            <Center py="xl">
              <Text c="dimmed" size="sm">
                {searching ? t('channel.noMessagesSearch') : t('channel.noMessages')}
              </Text>
            </Center>
          )}

          {!loading &&
            messages.map((m, i) => {
              const previous = messages[i - 1]
              const startsDay = !previous || !isSameDay(previous.createdAt, m.createdAt)
              return (
                <div key={m.id} style={{ display: 'contents' }}>
                  {startsDay && <DateSeparator iso={m.createdAt} />}
                  {m.id === firstUnreadId && <UnreadDivider />}
                  <MessageBubble
                    message={m}
                    active={m.id === activeMessageId}
                    onOpenDiscussion={onOpenDiscussion}
                  />
                </div>
              )
            })}
        </div>
      </div>

      <JumpToBottom visible={!atBottom} count={unseenCount} onClick={onJumpToBottom} />
    </div>
  )
}
