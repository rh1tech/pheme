import { useCallback, useEffect, useRef, useState } from 'react'
import { Outlet, useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { loadLastSeen } from '../../lib/lastSeen'
import { useEventStream } from '../../hooks/useEventStream'
import { useChatScroll } from '../../hooks/useChatScroll'
import type { ChatOutletContext } from '../../components/chat/context'
import { ChatHeader } from '../../components/chat/ChatHeader'
import { MessageFeed } from '../../components/chat/MessageFeed'
import { Composer } from '../../components/chat/Composer'
import { ReadOnlyNotice } from '../../components/chat/ReadOnlyNotice'
import { ChannelInfoPanel } from '../../components/chat/ChannelInfoPanel'
import type { Channel, ChannelRelation, Message } from '../../lib/types'

const PAGE_SIZE = 50
// A send is answered 202: the dispatcher writes the message, which reaches us
// over the live stream. If the stream is degraded the bubble would never appear,
// so a refetch backstops it after this long.
const SEND_SETTLE_MS = 2000

/**
 * The conversation: a channel's messages, oldest at the top and newest at the
 * bottom, plus whichever third column is open — a message's discussion (a nested
 * route) or the channel-info panel.
 */
export function ConversationRoute() {
  const { id = '', messageId } = useParams()
  const { list, composerFocus } = useOutletContext<ChatOutletContext>()
  const { t } = useTranslation()
  const navigate = useNavigate()

  const [channel, setChannel] = useState<Channel | null>(null)
  const [relation, setRelation] = useState<ChannelRelation | null>(null)
  // Oldest-first: the order the feed renders and the reader reads.
  const [messages, setMessages] = useState<Message[]>([])
  const [loading, setLoading] = useState(true)
  const [cursor, setCursor] = useState('')
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [infoOpen, setInfoOpen] = useState(false)
  const [unseen, setUnseen] = useState(0)
  // The message the unread divider sits above, and the anchor the feed opens on.
  const [firstUnreadId, setFirstUnreadId] = useState<string | undefined>(undefined)
  const pendingAnchor = useRef<string | null>(null)

  const [searching, setSearching] = useState(false)
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')

  // Opening another channel resets the view. Adjusting state during render is the
  // sanctioned alternative to a setState inside an effect, which would paint the
  // previous channel's search box for a frame before clearing it.
  const [openId, setOpenId] = useState(id)
  if (openId !== id) {
    setOpenId(id)
    setSearching(false)
    setSearch('')
    setQuery('')
    setUnseen(0)
    setFirstUnreadId(undefined)
  }

  // The reach-bottom callback needs the newest message, but must not re-subscribe
  // the scroll listener on every new message — hence a ref.
  const messagesRef = useRef(messages)
  useEffect(() => {
    messagesRef.current = messages
  })

  const markNewestRead = useCallback(() => {
    const newest = messagesRef.current[messagesRef.current.length - 1]
    if (newest) list.markRead(id, newest.createdAt)
  }, [id, list])

  const onReachBottom = useCallback(() => {
    setUnseen(0)
    markNewestRead()
  }, [markNewestRead])

  const { scrollerRef, contentRef, atBottom, scrollToBottom, scrollToMessage, captureAnchor } =
    useChatScroll(id, onReachBottom)

  useEffect(() => {
    let active = true
    api
      .getChannel(id)
      .then((rel) => {
        if (!active) return
        setChannel(rel.channel)
        setRelation(rel)
      })
      .catch(() => {
        if (!active) return
        setChannel(null)
        setRelation(null)
      })
    return () => {
      active = false
    }
  }, [id])

  // The server returns messages newest-first; the feed reads oldest-first.
  const loadPage = useCallback(
    async (q: string): Promise<void> => {
      setLoading(true)
      try {
        const page = await api.listMessages(id, '', q, PAGE_SIZE)
        setMessages(page.messages.slice().reverse())
        setCursor(page.nextCursor)
      } catch (e) {
        notifyError(t('dashboard.loadFailed'), e)
      } finally {
        setLoading(false)
      }
    },
    [id, t],
  )

  // Where a channel opens, following Telegram (bubbles.ts, the readMaxId branch of
  // performHistoryResult): at the first unread message, not at the newest one —
  // otherwise a reader with a backlog is dropped at the end of it with no way back
  // to where they stopped. Telegram makes one exception, which is kept here: with
  // exactly one unread message there is nothing to come back to, so it just goes
  // to the bottom.
  useEffect(() => {
    let active = true
    const run = async () => {
      setLoading(true)
      // Read before anything marks the channel read, or the divider can never appear.
      const seenAt = loadLastSeen()[id] ?? ''
      try {
        const page = await api.listMessages(id, '', '', PAGE_SIZE)
        if (!active) return
        const ordered = page.messages.slice().reverse()
        setMessages(ordered)
        setCursor(page.nextCursor)

        const unread = ordered.filter((m) => m.createdAt > seenAt)
        if (unread.length > 1) {
          setFirstUnreadId(unread[0].id)
          pendingAnchor.current = unread[0].id
        } else {
          setFirstUnreadId(undefined)
          const newest = ordered[ordered.length - 1]
          if (newest) list.markRead(id, newest.createdAt)
        }
      } catch (e) {
        if (active) notifyError(t('dashboard.loadFailed'), e)
      } finally {
        if (active) setLoading(false)
      }
    }
    void run()
    return () => {
      active = false
    }
    // `list` is stable enough for this purpose and would otherwise reload the
    // feed every time the sidebar re-sorts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, t])

  // The anchor can only be applied once the bubbles exist.
  useEffect(() => {
    const target = pendingAnchor.current
    if (!target || messages.length === 0) return
    pendingAnchor.current = null
    scrollToMessage(target)
  }, [messages, scrollToMessage])

  // The cursor walks backward in time, so each further page is *older* than what
  // is on screen and belongs above it.
  async function loadOlder() {
    if (!cursor || loadingOlder) return
    setLoadingOlder(true)
    captureAnchor()
    try {
      const page = await api.listMessages(id, cursor, query, PAGE_SIZE, true)
      setMessages((prev) => [...page.messages.slice().reverse(), ...prev])
      setCursor(page.nextCursor)
    } catch (e) {
      notifyError(t('dashboard.loadFailed'), e)
    } finally {
      setLoadingOlder(false)
    }
  }

  useEventStream((e) => {
    if (e.channelId !== id) return
    // Search results are a snapshot of a query, not the live tail: appending to
    // them would show a message that does not match what was searched for.
    if (query) return
    setMessages((prev) =>
      prev.some((m) => m.id === e.message.id) ? prev : [...prev, e.message],
    )
    // No manual scroll here: while the reader is at the bottom the feed is glued
    // there, so the new bubble pulls it down as soon as it lays out.
    if (atBottom) {
      list.markRead(id, e.message.createdAt)
    } else {
      setUnseen((n) => n + 1)
    }
  })

  function runSearch() {
    const q = search.trim()
    setQuery(q)
    void loadPage(q)
  }

  function closeSearch() {
    setSearching(false)
    setSearch('')
    if (query) {
      setQuery('')
      void loadPage('')
    }
  }

  // The two right-hand panes compete for the same column, so opening the info
  // panel first leaves the discussion route.
  function toggleInfo() {
    if (messageId) {
      navigate(`/channels/${id}`)
      setInfoOpen(true)
      return
    }
    setInfoOpen((open) => !open)
  }

  function openDiscussion(mid: string) {
    setInfoOpen(false)
    navigate(`/channels/${id}/messages/${mid}`)
  }

  // Backstop a stalled live stream: a send is only acknowledged, so if the message
  // has not streamed in by now, reload the newest page rather than leave the
  // sender staring at a feed that looks like it dropped their message.
  function onSent() {
    window.setTimeout(() => {
      if (query) return
      void api
        .listMessages(id, '', '', PAGE_SIZE, true)
        .then((page) => {
          const fresh = page.messages.slice().reverse()
          const newest = fresh[fresh.length - 1]
          if (!newest) return
          setMessages((prev) => (prev.some((m) => m.id === newest.id) ? prev : fresh))
        })
        .catch(() => undefined)
    }, SEND_SETTLE_MS)
  }

  const canModerate = relation
    ? (relation.isOwner || relation.role === 'admin') && relation.status === 'active'
    : false

  return (
    <>
      <section className="pheme-conversation">
        <ChatHeader
          channel={channel}
          channelId={id}
          searching={searching}
          search={search}
          onSearchChange={setSearch}
          onSearchSubmit={runSearch}
          onSearchOpen={() => setSearching(true)}
          onSearchClose={closeSearch}
          onToggleInfo={toggleInfo}
        />

        <MessageFeed
          messages={messages}
          loading={loading}
          loadingOlder={loadingOlder}
          hasOlder={Boolean(cursor)}
          onLoadOlder={loadOlder}
          scrollerRef={scrollerRef}
          contentRef={contentRef}
          atBottom={atBottom}
          onJumpToBottom={() => scrollToBottom('smooth')}
          unseenCount={unseen}
          activeMessageId={messageId}
          firstUnreadId={firstUnreadId}
          onOpenDiscussion={openDiscussion}
          searching={Boolean(query)}
        />

        {canModerate ? (
          <Composer channelId={id} focusSignal={composerFocus} onSent={onSent} />
        ) : (
          <ReadOnlyNotice />
        )}
      </section>

      {/* The discussion is a nested route, so opening one does not remount the
          conversation (which would drop the feed and its scroll position). It
          renders nothing when no message is selected. */}
      <Outlet context={relation} />

      {!messageId && infoOpen && channel && (
        <ChannelInfoPanel
          channelId={id}
          channel={channel}
          isOwner={relation?.isOwner ?? false}
          canModerate={canModerate}
          onClose={() => setInfoOpen(false)}
          onChannelChanged={setChannel}
          onListChanged={list.refresh}
        />
      )}
    </>
  )
}
