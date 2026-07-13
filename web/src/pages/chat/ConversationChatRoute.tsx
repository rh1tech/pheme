import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Group, Stack, Text, Textarea } from '@mantine/core'
import { IconArrowLeft, IconSend } from '@tabler/icons-react'
import type { KeyboardEvent } from 'react'
import { useMediaQuery } from '@mantine/hooks'
import { useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { decodeChatContent, encodeChatContent } from '../../lib/chatContent'
import { conversationTitle, otherMember } from '../../lib/conversation'
import { messageTime } from '../../lib/time'
import { useAuth } from '../../auth/context'
import { useEventStream } from '../../hooks/useEventStream'
import { useChatScroll } from '../../hooks/useChatScroll'
import { useEdgeSwipeBack } from '../../hooks/useEdgeSwipeBack'
import { ChannelAvatar } from '../../components/chat/ChannelAvatar'
import { DateSeparator } from '../../components/chat/DateSeparator'
import { JumpToBottom } from '../../components/chat/JumpToBottom'
import { ChatSkeleton } from '../../components/chat/ChatSkeleton'
import { isSameDay } from '../../lib/time'
import type { ChatOutletContext } from '../../components/chat/context'
import type { ChatMessage, Conversation } from '../../lib/types'

const PAGE_SIZE = 50

/**
 * A private conversation — the encrypted-chat counterpart of ConversationRoute
 * (channels). It reuses the feed's scroll engine and layout, but its own bubble:
 * chat messages are two-sided (own vs others) and their content is decoded
 * through lib/chatContent, the one seam where MLS decryption will slot in.
 */
export function ConversationChatRoute() {
  const { id = '' } = useParams()
  const { conversations, composerFocus } = useOutletContext<ChatOutletContext>()
  const { userId } = useAuth()
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const isMobile = useMediaQuery('(max-width: 48em)')

  const [conversation, setConversation] = useState<Conversation | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([]) // oldest-first
  const [loading, setLoading] = useState(true)
  const [cursor, setCursor] = useState('')
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [unseen, setUnseen] = useState(0)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const textRef = useRef<HTMLTextAreaElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)

  const atBottomRef = useRef(true)

  const markNewestRead = useCallback(() => {
    const newest = messages[messages.length - 1]
    if (newest) conversations.markRead(id, newest.createdAt)
  }, [messages, id, conversations])

  const { scrollerRef, contentRef, atBottom, scrollToBottom, captureAnchor } = useChatScroll(
    id,
    () => {
      setUnseen(0)
      markNewestRead()
    },
  )
  useEffect(() => {
    atBottomRef.current = atBottom
  })

  useEdgeSwipeBack(Boolean(isMobile), useCallback(() => navigate('/'), [navigate]))

  useEffect(() => {
    if (!isMobile) textRef.current?.focus()
  }, [id, composerFocus, isMobile])

  useEffect(() => {
    let active = true
    api
      .getConversation(id)
      .then((c) => active && setConversation(c))
      .catch(() => active && setConversation(null))
    return () => {
      active = false
    }
  }, [id])

  useEffect(() => {
    let active = true
    const run = async () => {
      setLoading(true)
      try {
        const page = await api.listChatMessages(id, '', PAGE_SIZE)
        if (!active) return
        setMessages(page.messages.slice().reverse())
        setCursor(page.nextCursor)
        const newest = page.messages[0]
        if (newest) conversations.markRead(id, newest.createdAt)
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  async function loadOlder() {
    if (!cursor || loadingOlder) return
    setLoadingOlder(true)
    captureAnchor()
    try {
      const page = await api.listChatMessages(id, cursor, PAGE_SIZE, true)
      setMessages((prev) => [...page.messages.slice().reverse(), ...prev])
      setCursor(page.nextCursor)
    } catch (e) {
      notifyError(t('dashboard.loadFailed'), e)
    } finally {
      setLoadingOlder(false)
    }
  }

  useEffect(() => {
    const el = sentinelRef.current
    const root = scrollerRef.current
    if (!el || !root || !cursor) return
    const observer = new IntersectionObserver(
      (entries) => entries.some((e) => e.isIntersecting) && loadOlder(),
      { root, rootMargin: '300px 0px 0px 0px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cursor, scrollerRef])

  useEventStream((e) => {
    if (e.conversationId !== id || !e.chatMessage) return
    const msg = e.chatMessage
    setMessages((prev) => (prev.some((m) => m.id === msg.id) ? prev : [...prev, msg]))
    if (atBottomRef.current) {
      conversations.markRead(id, msg.createdAt)
    } else {
      setUnseen((n) => n + 1)
    }
  })

  const canSend = draft.trim().length > 0

  async function send() {
    if (!canSend || sending) return
    setSending(true)
    try {
      // Encode through the crypto seam. Today plaintext; MLS in Phase 3.
      const { ciphertext, contentType } = encodeChatContent({ body: draft.trim() })
      const msg = await api.sendChatMessage(id, ciphertext, contentType)
      setDraft('')
      // Optimistic append; the SSE echo is deduped by id.
      setMessages((prev) => (prev.some((m) => m.id === msg.id) ? prev : [...prev, msg]))
      requestAnimationFrame(() => scrollToBottom('smooth'))
      textRef.current?.focus()
    } catch (e) {
      notifyError(t('channel.sendFailed'), e)
    } finally {
      setSending(false)
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key !== 'Enter' || e.shiftKey || e.nativeEvent.isComposing || isMobile) return
    e.preventDefault()
    void send()
  }

  const title = conversation ? conversationTitle(conversation, userId ?? '') : ''
  const avatarId =
    conversation?.kind === 'direct' ? otherMember(conversation, userId ?? '')?.avatarId : conversation?.avatarId
  const isGroup = conversation?.kind === 'group'

  return (
    <section className="pheme-conversation">
      <header className="pheme-chat-header" data-testid="chat-header">
        <Group gap="sm" wrap="nowrap">
          <ActionIcon
            hiddenFrom="sm"
            variant="subtle"
            color="gray"
            aria-label={t('chat.back')}
            onClick={() => navigate('/')}
          >
            <IconArrowLeft size={20} />
          </ActionIcon>
          <ChannelAvatar id={conversation?.id ?? id} name={title || '·'} avatarId={avatarId} size={38} />
          <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
            <Text fw={600} size="sm" truncate>
              {title}
            </Text>
            {isGroup && (
              <Text size="xs" c="dimmed">
                {t('chat.memberCount', { count: conversation?.members.length ?? 0 })}
              </Text>
            )}
          </Stack>
        </Group>
      </header>

      <div className="pheme-feed-wrap">
        <div className="pheme-feed" ref={scrollerRef} data-testid="chat-feed">
          <div className="pheme-feed-content" ref={contentRef}>
            {cursor && <div ref={sentinelRef} aria-hidden />}
            {loading && <ChatSkeleton />}
            {!loading && messages.length === 0 && (
              <Text c="dimmed" size="sm" ta="center" py="xl">
                {t('chat.noChatMessages')}
              </Text>
            )}
            {!loading &&
              messages.map((m, i) => {
                const prev = messages[i - 1]
                const startsDay = !prev || !isSameDay(prev.createdAt, m.createdAt)
                const own = m.senderId === userId
                const content = decodeChatContent(m.ciphertext, m.contentType)
                const senderName = isGroup
                  ? conversation?.members.find((mem) => mem.userId === m.senderId)?.user
                  : undefined
                return (
                  <div key={m.id} style={{ display: 'contents' }}>
                    {startsDay && <DateSeparator iso={m.createdAt} />}
                    <div
                      className="pheme-bubble pheme-chat-bubble"
                      data-own={own}
                      data-testid="chat-message"
                    >
                      {isGroup && !own && senderName && (
                        <Text size="xs" fw={600} c="iris">
                          {senderName.displayName || senderName.username || 'User'}
                        </Text>
                      )}
                      <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>
                        {content?.body ?? '…'}
                      </Text>
                      <div className="pheme-bubble-footer">
                        <Text size="xs" c="dimmed">
                          {messageTime(m.createdAt, i18n.language)}
                        </Text>
                      </div>
                    </div>
                  </div>
                )
              })}
          </div>
        </div>
        <JumpToBottom visible={!atBottom} count={unseen} onClick={() => scrollToBottom('smooth')} />
      </div>

      <div className="pheme-composer" data-testid="composer">
        <Group gap="xs" align="flex-end" wrap="nowrap">
          <Textarea
            ref={textRef}
            aria-label={t('channel.body')}
            placeholder={t('channel.composerPlaceholder')}
            data-testid="composer-body"
            autosize
            minRows={1}
            maxRows={8}
            value={draft}
            onChange={(e) => setDraft(e.currentTarget.value)}
            onKeyDown={onKeyDown}
            style={{ flex: 1 }}
          />
          <ActionIcon
            aria-label={t('channel.send')}
            variant="filled"
            size="lg"
            loading={sending}
            disabled={!canSend}
            onClick={send}
          >
            <IconSend size={18} />
          </ActionIcon>
        </Group>
      </div>
    </section>
  )
}
