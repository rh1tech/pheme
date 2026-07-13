import { useCallback, useEffect, useRef, useState } from 'react'
import { Text } from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { Outlet, useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { loadLastSeen } from '../../lib/lastSeen'
import { useEventStream } from '../../hooks/useEventStream'
import { useChatScroll } from '../../hooks/useChatScroll'
import { useEdgeSwipeBack } from '../../hooks/useEdgeSwipeBack'
import type { ChatOutletContext } from '../../components/chat/context'
import { ChatHeader } from '../../components/chat/ChatHeader'
import { MessageFeed } from '../../components/chat/MessageFeed'
import { Composer } from '../../components/chat/Composer'
import { ReadOnlyNotice } from '../../components/chat/ReadOnlyNotice'
import { ChannelInfoPanel } from '../../components/chat/ChannelInfoPanel'
import { MediaViewer, type MediaViewerTarget } from '../../components/chat/MediaViewer'
import { MessageMenu, type MenuTarget } from '../../components/chat/MessageMenu'
import { ConfirmModal } from '../../components/ConfirmModal'
import { notifySuccess } from '../../lib/notify'
import type { Channel, ChannelRelation, Message, MessageImage } from '../../lib/types'

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
  // The list already knows this channel's name and avatar; use them for the header
  // until the fetch lands, so it shows the real channel at once instead of the
  // "Channel" fallback and a placeholder colour.
  const knownChannel = list.channels.find((c) => c.id === id)
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
  const [media, setMedia] = useState<MediaViewerTarget | null>(null)
  const [menu, setMenu] = useState<MenuTarget | null>(null)
  const [pendingDelete, setPendingDelete] = useState<Message | null>(null)
  const [deleting, setDeleting] = useState(false)

  const [searching, setSearching] = useState(false)
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  // Search finds messages; it does not replace the feed with them. The hits are a
  // list to step through, and stepping jumps the live feed to that message in its
  // own surroundings — the point of finding it is usually to read what was around it.
  const [hits, setHits] = useState<Message[]>([])
  const [hitIndex, setHitIndex] = useState(0)
  const [highlightId, setHighlightId] = useState<string | undefined>(undefined)

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
    setHits([])
    setHitIndex(0)
    setHighlightId(undefined)
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

  // On a phone the conversation covers the whole screen, and an installed PWA has
  // no browser back button — so the edge swipe is the way out of a channel.
  const isMobile = useMediaQuery('(max-width: 48em)')
  const backToList = useCallback(() => navigate('/'), [navigate])
  useEdgeSwipeBack(Boolean(isMobile) && !messageId && !infoOpen, backToList)

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
      const page = await api.listMessages(id, cursor, '', PAGE_SIZE, true)
      setMessages((prev) => [...page.messages.slice().reverse(), ...prev])
      setCursor(page.nextCursor)
    } catch (e) {
      notifyError(t('dashboard.loadFailed'), e)
    } finally {
      setLoadingOlder(false)
    }
  }

  useEventStream((e) => {
    if (e.channelId !== id || !e.message) return
    const incoming = e.message
    setMessages((prev) =>
      prev.some((m) => m.id === incoming.id) ? prev : [...prev, incoming],
    )
    // No manual scroll here: while the reader is at the bottom the feed is glued
    // there, so the new bubble pulls it down as soon as it lays out.
    if (atBottom) {
      list.markRead(id, incoming.createdAt)
    } else {
      setUnseen((n) => n + 1)
    }
  })

  async function confirmDelete() {
    const doomed = pendingDelete
    if (!doomed) return
    setDeleting(true)
    try {
      await api.deleteMessage(id, doomed.id)
      setMessages((prev) => prev.filter((m) => m.id !== doomed.id))
      setPendingDelete(null)
      notifySuccess(t('channel.messageDeleted'))
      // The sidebar's preview may have been this message.
      void list.refresh()
      // Its discussion pane, if open, now points at nothing.
      if (messageId === doomed.id) navigate(`/channels/${id}`)
    } catch (e) {
      notifyError(t('channel.deleteFailed'), e)
    } finally {
      setDeleting(false)
    }
  }

  /** Loads the conversation around a message and lights it up. */
  const jumpTo = useCallback(
    async (message: Message) => {
      try {
        const page = await api.messagesAround(id, message.id, PAGE_SIZE)
        setMessages(page.messages.slice().reverse())
        setCursor(page.nextCursor)
        setHighlightId(message.id)
        pendingAnchor.current = message.id
      } catch (e) {
        notifyError(t('dashboard.loadFailed'), e)
      }
    },
    [id, t],
  )

  async function runSearch() {
    const q = search.trim()
    if (!q) return
    setQuery(q)
    try {
      const page = await api.listMessages(id, '', q, PAGE_SIZE)
      // The API answers newest-first; step through hits newest to oldest.
      setHits(page.messages)
      setHitIndex(0)
      if (page.messages.length > 0) await jumpTo(page.messages[0])
    } catch (e) {
      notifyError(t('channel.searchFailed'), e)
    }
  }

  function stepHit(delta: number) {
    if (hits.length === 0) return
    const next = (hitIndex + delta + hits.length) % hits.length
    setHitIndex(next)
    void jumpTo(hits[next])
  }

  function closeSearch() {
    setSearching(false)
    setSearch('')
    setQuery('')
    setHits([])
    setHitIndex(0)
    setHighlightId(undefined)
    // Back to the live tail the reader was on before they went looking.
    void loadPage('')
    scrollToBottom()
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
      // Only reconcile the tail for a reader who is looking at it. Someone who has
      // jumped to a search hit must not be dragged back to the newest message.
      if (!atBottom) return
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
          channel={channel?.id === id ? channel : null}
          channelId={id}
          hintName={knownChannel?.name}
          hintAvatarId={knownChannel?.avatarId}
          searching={searching}
          search={search}
          onSearchChange={setSearch}
          onSearchSubmit={runSearch}
          onSearchOpen={() => setSearching(true)}
          onSearchClose={closeSearch}
          hitCount={hits.length}
          hitIndex={hitIndex}
          onPrevHit={() => stepHit(1)}
          onNextHit={() => stepHit(-1)}
          onToggleInfo={toggleInfo}
        />

        <MessageFeed
          messages={messages}
          loading={loading}
          loadingOlder={loadingOlder}
          hasOlder={Boolean(cursor)}
          olderCursor={cursor}
          onLoadOlder={loadOlder}
          scrollerRef={scrollerRef}
          contentRef={contentRef}
          atBottom={atBottom}
          onJumpToBottom={() => scrollToBottom('smooth')}
          unseenCount={unseen}
          activeMessageId={messageId}
          firstUnreadId={firstUnreadId}
          highlightId={highlightId}
          onOpenDiscussion={openDiscussion}
          onOpenMedia={(images: MessageImage[], index: number) => setMedia({ images, index })}
          onOpenMenu={(message, x, y) => setMenu({ message, x, y })}
          searching={Boolean(query) && hits.length === 0}
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

      {media && <MediaViewer target={media} onClose={() => setMedia(null)} />}

      {menu && (
        <MessageMenu
          target={menu}
          channelId={id}
          canModerate={canModerate}
          onClose={() => setMenu(null)}
          onOpenDiscussion={openDiscussion}
          onDelete={setPendingDelete}
        />
      )}

      <ConfirmModal
        opened={pendingDelete !== null}
        onClose={() => setPendingDelete(null)}
        onConfirm={confirmDelete}
        title={t('channel.deleteMessage')}
        confirmLabel={t('common.delete')}
        loading={deleting}
      >
        <Text size="sm">{t('channel.deleteMessageConfirm')}</Text>
      </ConfirmModal>

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
