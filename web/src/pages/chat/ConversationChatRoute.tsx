import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Alert, Group, Menu, Stack, Text, Textarea } from '@mantine/core'
import { IconArrowLeft, IconDots, IconLock, IconLogout, IconSend, IconShieldLock, IconTrash, IconUsers } from '@tabler/icons-react'
import type { KeyboardEvent } from 'react'
import { useMediaQuery } from '@mantine/hooks'
import { useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { deserializeContent, serializeContent } from '../../lib/chatContent'
import {
  MLS_APPLICATION,
  MLS_CONTROL_TYPES,
  MLS_DEVICE,
  PeerKeysMissingError,
  admitAnnouncedDevice,
  base64ToBytes,
  ensureGroup,
  mlsSession,
  removeGroupMember,
} from '../../lib/mls'
import { cacheBody, loadCachedBodies, setPreview } from '../../lib/chatCache'
import { conversationAvatarKey, conversationTitle, otherMember } from '../../lib/conversation'
import { messageTime } from '../../lib/time'
import { useAuth } from '../../auth/context'
import { useEventStream } from '../../hooks/useEventStream'
import { useChatScroll } from '../../hooks/useChatScroll'
import { useEdgeSwipeBack } from '../../hooks/useEdgeSwipeBack'
import { ChannelAvatar } from '../../components/chat/ChannelAvatar'
import { DateSeparator } from '../../components/chat/DateSeparator'
import { JumpToBottom } from '../../components/chat/JumpToBottom'
import { ChatSkeleton } from '../../components/chat/ChatSkeleton'
import { SafetyNumberModal } from '../../components/chat/SafetyNumber'
import { GroupMembersModal } from '../../components/chat/GroupMembersModal'
import { ConfirmModal } from '../../components/ConfirmModal'
import { isSameDay } from '../../lib/time'
import type { ChatOutletContext } from '../../components/chat/context'
import type { ChatMessage, Conversation } from '../../lib/types'

const PAGE_SIZE = 50

/**
 * A private conversation — the encrypted-chat counterpart of ConversationRoute
 * (channels). It reuses the feed's scroll engine and layout, but its own bubble:
 * chat messages are two-sided (own vs others) and their content is E2E encrypted
 * with MLS (lib/mls).
 *
 * Decryption is one-shot: MLS deletes each message key after use (forward
 * secrecy), and a sender cannot decrypt their own message at all. So every body is
 * recorded in a local plaintext cache (lib/chatCache) the first time it is seen —
 * on send for our own messages, on receive for others' — and history renders from
 * that cache, never by decrypting twice.
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
  const [bodies, setBodies] = useState<Record<string, string>>({}) // decrypted, by id
  // The conversation id whose local body cache has finished loading; message
  // processing waits for this so cached messages are never re-decrypted.
  const [readyId, setReadyId] = useState('')
  const [loading, setLoading] = useState(true)
  const [cursor, setCursor] = useState('')
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [unseen, setUnseen] = useState(0)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [safetyOpen, setSafetyOpen] = useState(false)
  const [membersOpen, setMembersOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [confirmLeave, setConfirmLeave] = useState(false)
  const [actionBusy, setActionBusy] = useState(false)
  // The conversation whose peer has published no keys (so we cannot encrypt to them).
  // Keyed by id and derived, so switching chats clears the banner without a reset.
  const [peerNotReadyId, setPeerNotReadyId] = useState('')
  const peerNotReady = peerNotReadyId === id
  const textRef = useRef<HTMLTextAreaElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)

  const atBottomRef = useRef(true)
  // The decrypted-body cache mirrored into a ref, plus the set of message ids we
  // have already handled — so the one-shot decrypt never runs twice for a message.
  const bodiesRef = useRef<Record<string, string>>({})
  const processedRef = useRef<Set<string>>(new Set())
  // The conversation's MLS group, once this device is a member of it. Empty while it is
  // not — a device that has just signed in has to wait for a member to admit it, and
  // until then there is nothing to encrypt to and nothing it can read.
  //
  // Keyed by conversation and derived at render, like `peerNotReadyId` and `header`: the
  // component is reused across chats, and a group id left over from the previous one would
  // be a group this conversation's messages were never encrypted to.
  const [settled, setSettled] = useState({ conversationId: '', groupId: '' })
  const groupId = settled.conversationId === id ? settled.groupId : ''
  const setGroupId = useCallback(
    (gid: string) => setSettled({ conversationId: id, groupId: gid }),
    [id],
  )

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
      .then((c) => {
        if (!active) return
        setConversation(c)
        if (!userId) return
        // Settle the group: establish it if it does not exist and we created the
        // conversation, add any member device that is missing from it, and — if this
        // device is not in it — ask to be let in. A null group id is not a failure; it
        // means "not admitted yet", and a member will admit us.
        ensureGroup(c, userId)
          .then((gid) => {
            if (!active) return
            setGroupId(gid ?? '')
            setPeerNotReadyId('')
          })
          .catch((e: unknown) => {
            // The other person has published no keys on any device, so nothing can be
            // encrypted to them. Say so plainly, where the user is about to type, rather
            // than letting them discover it by having a message fail to send.
            if (active) setPeerNotReadyId(e instanceof PeerKeysMissingError ? id : '')
          })
      })
      .catch(() => active && setConversation(null))
    return () => {
      active = false
    }
  }, [id, userId, setGroupId])

  // Preload this conversation's already-decrypted bodies from the local cache
  // before any message processing, so cached messages are never re-decrypted.
  useEffect(() => {
    let active = true
    bodiesRef.current = {}
    processedRef.current = new Set()
    loadCachedBodies(id).then((cached) => {
      if (!active) return
      bodiesRef.current = cached
      for (const messageId of Object.keys(cached)) processedRef.current.add(messageId)
      setBodies(cached)
      setReadyId(id)
    })
    return () => {
      active = false
    }
  }, [id])

  // Decrypt every message not yet handled, in order. Runs whenever the message list
  // grows (initial load, pagination, live arrival) once the cache is loaded and this
  // device is actually in the group.
  //
  // Control messages are NOT handled here any more. Joining and applying Commits is the
  // group's business, not the view's: it has to happen in epoch order, it has to happen
  // even for conversations nobody has open, and getting it wrong forks the device off the
  // group. lib/mls owns all of it (ensureGroup → catchUp).
  useEffect(() => {
    if (readyId !== id || !userId || !conversation || !groupId || messages.length === 0) return
    let active = true
    const run = async () => {
      let session: Awaited<ReturnType<typeof mlsSession>>
      try {
        session = await mlsSession(userId)
      } catch {
        // This device has no keys yet and a backup is waiting to be restored (or the
        // WASM failed to load). Either way there is nothing to decrypt with; the
        // restore prompt is what resolves it.
        return
      }
      const next = { ...bodiesRef.current }
      let changed = false
      for (const m of messages) {
        if (processedRef.current.has(m.id) || MLS_CONTROL_TYPES.has(m.contentType)) continue
        processedRef.current.add(m.id)
        try {
          if (m.contentType === MLS_APPLICATION) {
            const bytes = await session.decrypt(groupId, m.ciphertext)
            const content = bytes && deserializeContent(bytes)
            if (content) {
              next[m.id] = content.body
              changed = true
              void cacheBody(id, m.id, content.body)
              setPreview(id, content.body)
            }
          } else {
            // Legacy plaintext (pre-encryption messages on the wire).
            const content = deserializeContent(base64ToBytes(m.ciphertext))
            if (content) {
              next[m.id] = content.body
              changed = true
            }
          }
        } catch {
          // Decryption can legitimately fail: it is our own message (a sender can never
          // decrypt their own), it predates this device joining the group (MLS gives a
          // new member no access to what was said before it arrived), or another tab
          // decrypted it first — MLS lets exactly one of them succeed. In the last two
          // cases the plaintext may be in the local cache, so read that rather than
          // leaving a blank. Never retry the decrypt itself.
          const cached = await loadCachedBodies(id)
          if (cached[m.id]) {
            next[m.id] = cached[m.id]
            changed = true
          }
        }
      }

      if (active && changed) {
        bodiesRef.current = next
        setBodies(next)
      }
    }
    void run()
    return () => {
      active = false
    }
  }, [messages, readyId, userId, id, conversation, groupId])

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
    if (e.conversationId !== id) return
    // Deleted out from under us (the other party, or a group admin): leave.
    if (e.conversationDeleted) {
      navigate('/')
      return
    }
    if (!e.chatMessage) return
    const msg = e.chatMessage

    // MLS protocol traffic. It is not a message, and it never reaches the transcript —
    // but it is how the group changes shape, so it has to be acted on the moment it
    // arrives rather than the next time somebody reloads.
    if (MLS_CONTROL_TYPES.has(msg.contentType)) {
      if (!userId || !conversation) return
      if (msg.contentType === MLS_DEVICE && msg.senderId !== userId) {
        // Another member signed in somewhere new and cannot read the conversation. Add
        // their device. Everyone with the chat open will try; the server lets exactly one
        // Commit through and the rest find nothing left to do.
        void admitAnnouncedDevice(conversation, userId).catch(() => {})
        return
      }
      // A Welcome or a Commit: catch up on it. If it is the Welcome that admits THIS
      // device, settling now is what turns the conversation from unreadable to readable
      // without the user having to reload.
      void ensureGroup(conversation, userId)
        .then((gid) => setGroupId(gid ?? ''))
        .catch(() => {})
      return
    }

    setMessages((prev) => (prev.some((m) => m.id === msg.id) ? prev : [...prev, msg]))
    if (atBottomRef.current) {
      conversations.markRead(id, msg.createdAt)
    } else {
      setUnseen((n) => n + 1)
    }
  })

  async function reloadConversation() {
    const c = await api.getConversation(id)
    setConversation(c)
  }

  async function deleteChat() {
    setActionBusy(true)
    try {
      await api.deleteConversation(id)
      navigate('/')
    } catch (e) {
      notifyError(t('group.actionFailed'), e)
      setActionBusy(false)
    }
  }

  async function leaveGroup() {
    if (!userId) return
    setActionBusy(true)
    try {
      await removeGroupMember(id, userId, userId)
      navigate('/')
    } catch (e) {
      notifyError(t('group.actionFailed'), e)
      setActionBusy(false)
    }
  }

  // What the header and its controls read: the fetched conversation once it is for
  // THIS id, else the list's copy. The list already has name, members and avatar, so
  // opening a chat shows the right header instantly instead of flashing a placeholder.
  // Derived at render, so it tracks the current id even though the component is reused.
  const header =
    conversation?.id === id
      ? conversation
      : (conversations.conversations.find((c) => c.id === id)?.conversation ?? null)

  const meMember = header?.members.find((m) => m.userId === userId)
  const iAmAdmin = meMember?.role === 'admin'

  const canSend = draft.trim().length > 0

  async function send() {
    if (!canSend || sending || !userId || !conversation) return
    const body = draft.trim()
    setSending(true)
    try {
      const session = await mlsSession(userId)
      // Settle the group before encrypting: it may not exist yet (we created the
      // conversation and this is the first message), or a member may have signed in on a
      // new device that has to be added before it can read what we are about to send.
      const gid = groupId || (await ensureGroup(conversation, userId))
      if (!gid) throw new Error(t('chat.notJoined'))
      setGroupId(gid)

      const ciphertext = await session.encrypt(gid, serializeContent({ body }))
      const msg = await api.sendChatMessage(id, ciphertext, MLS_APPLICATION)

      // We can never decrypt our own MLS message, so record its plaintext now and
      // mark it handled so the SSE echo does not try (and fail) to decrypt it.
      processedRef.current.add(msg.id)
      bodiesRef.current = { ...bodiesRef.current, [msg.id]: body }
      setBodies(bodiesRef.current)
      void cacheBody(id, msg.id, body)
      setPreview(id, body)

      setDraft('')
      setMessages((prev) => (prev.some((m) => m.id === msg.id) ? prev : [...prev, msg]))
      requestAnimationFrame(() => scrollToBottom('smooth'))
      textRef.current?.focus()
    } catch (e) {
      if (e instanceof PeerKeysMissingError) {
        setPeerNotReadyId(id)
        notifyError(t('chat.peerNotReady'), null)
      } else {
        notifyError(t('channel.sendFailed'), e)
      }
    } finally {
      setSending(false)
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key !== 'Enter' || e.shiftKey || e.nativeEvent.isComposing || isMobile) return
    e.preventDefault()
    void send()
  }

  // MLS control messages carry no user-visible text.
  const visibleMessages = messages.filter((m) => !MLS_CONTROL_TYPES.has(m.contentType))

  const title = header ? conversationTitle(header, userId ?? '') : ''
  const avatarId =
    header?.kind === 'direct' ? otherMember(header, userId ?? '')?.avatarId : header?.avatarId
  // Same seed the sidebar uses, so a chat is the same colour in the list and inside it.
  const avatarKey = header ? conversationAvatarKey(header, userId ?? '') : id
  const isGroup = header?.kind === 'group'

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
          <ChannelAvatar id={avatarKey} name={title || '·'} avatarId={avatarId} size={38} />
          <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
            <Text fw={600} size="sm" truncate>
              {title}
            </Text>
            {isGroup && (
              <Text size="xs" c="dimmed">
                {t('chat.memberCount', { count: header?.members.length ?? 0 })}
              </Text>
            )}
          </Stack>
          <ActionIcon
            variant="subtle"
            color="gray"
            aria-label={t('safety.verify')}
            onClick={() => setSafetyOpen(true)}
          >
            <IconShieldLock size={20} />
          </ActionIcon>
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray" aria-label={t('chat.conversationMenu')}>
                <IconDots size={20} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              {isGroup && (
                <Menu.Item leftSection={<IconUsers size={18} />} onClick={() => setMembersOpen(true)}>
                  {t('group.membersTitle')}
                </Menu.Item>
              )}
              {isGroup && (
                <Menu.Item
                  leftSection={<IconLogout size={18} />}
                  onClick={() => setConfirmLeave(true)}
                >
                  {t('group.leave')}
                </Menu.Item>
              )}
              {/* A direct chat can be deleted by either party; a group only by an admin. */}
              {(!isGroup || iAmAdmin) && (
                <Menu.Item
                  color="red"
                  leftSection={<IconTrash size={18} />}
                  onClick={() => setConfirmDelete(true)}
                >
                  {isGroup ? t('group.deleteGroup') : t('chat.deleteChat')}
                </Menu.Item>
              )}
            </Menu.Dropdown>
          </Menu>
        </Group>
      </header>

      <SafetyNumberModal
        conversationId={id}
        opened={safetyOpen}
        onClose={() => setSafetyOpen(false)}
      />

      {header && isGroup && (
        <GroupMembersModal
          conversation={header}
          opened={membersOpen}
          onClose={() => setMembersOpen(false)}
          onChanged={reloadConversation}
        />
      )}

      <ConfirmModal
        opened={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={deleteChat}
        loading={actionBusy}
        title={isGroup ? t('group.deleteGroup') : t('chat.deleteChat')}
      >
        <Text size="sm">{isGroup ? t('group.deleteGroupConfirm') : t('chat.deleteChatConfirm')}</Text>
      </ConfirmModal>

      <ConfirmModal
        opened={confirmLeave}
        onClose={() => setConfirmLeave(false)}
        onConfirm={leaveGroup}
        loading={actionBusy}
        title={t('group.leave')}
        // Without this the button reads "Delete" — ConfirmModal's default, which is right
        // for the two dialogs above it and wrong here. Leaving a group deletes nothing.
        confirmLabel={t('group.leave')}
      >
        <Text size="sm">{t('group.leaveConfirm')}</Text>
      </ConfirmModal>

      <div className="pheme-feed-wrap">
        <div className="pheme-feed" ref={scrollerRef} data-testid="chat-feed">
          <div className="pheme-feed-content" ref={contentRef}>
            {cursor && <div ref={sentinelRef} aria-hidden />}
            {loading && <ChatSkeleton />}
            {!loading && visibleMessages.length === 0 && (
              <Text c="dimmed" size="sm" ta="center" py="xl">
                {t('chat.noChatMessages')}
              </Text>
            )}
            {!loading &&
              visibleMessages.map((m, i) => {
                const prev = visibleMessages[i - 1]
                const startsDay = !prev || !isSameDay(prev.createdAt, m.createdAt)
                const own = m.senderId === userId
                const body = bodies[m.id]
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
                      {body !== undefined ? (
                        <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>
                          {body}
                        </Text>
                      ) : (
                        // Not a loading state: this device cannot read this message and
                        // never will. MLS gives a device no access to what was said before
                        // it joined the group, so a message from before this browser or
                        // phone signed in stays sealed here even though it is perfectly
                        // readable on the device that received it.
                        //
                        // Saying that is the whole point. The old placeholder was an
                        // ellipsis, which reads as "still loading" — so a conversation that
                        // was permanently broken looked like one that was about to arrive,
                        // and nobody could tell the difference.
                        <Text size="sm" c="dimmed" fs="italic" data-testid="chat-message-sealed">
                          {t('chat.notAvailableOnThisDevice')}
                        </Text>
                      )}
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

      {/* data-joined is the honest answer to "can this device read and write this
          conversation yet?" — it is true only once the device holds the MLS group. The UI
          uses it for the banner below; tests use it to wait for a device to actually be
          admitted rather than for a spinner to stop. */}
      <div className="pheme-composer" data-testid="composer" data-joined={Boolean(groupId)}>
        {/* Nothing can be encrypted to someone who has published no keys. Say that here,
            where the user is about to type, rather than after their message fails. */}
        {peerNotReady && (
          <Alert
            variant="light"
            color="yellow"
            icon={<IconLock size={16} />}
            p="xs"
            mb="xs"
            data-testid="peer-not-ready"
          >
            <Text size="xs">{t('chat.peerNotReady')}</Text>
          </Alert>
        )}
        {/* This device is a member of the conversation but not yet of its encrypted group
            — it has just signed in, and a member has to admit it. It is not stuck, and it
            is not loading; it is waiting, and it will not be able to read what was said
            before it arrives. Saying so beats a composer that silently refuses to send. */}
        {!peerNotReady && !loading && !groupId && (
          <Alert
            variant="light"
            color="blue"
            icon={<IconLock size={16} />}
            p="xs"
            mb="xs"
            data-testid="device-joining"
          >
            <Text size="xs">{t('chat.joiningOnThisDevice')}</Text>
          </Alert>
        )}
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
            // Keep the keyboard up: a tap on the send button would otherwise blur the
            // textarea, and iOS will not restore focus programmatically after send()'s
            // awaits (that only works inside the original gesture). Preventing the
            // default on pointer-down stops the button from taking focus at all, so the
            // textarea keeps it and the next message can be typed straight away.
            onMouseDown={(e) => e.preventDefault()}
            onPointerDown={(e) => e.preventDefault()}
          >
            <IconSend size={18} />
          </ActionIcon>
        </Group>
      </div>
    </section>
  )
}
