import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Alert, CloseButton, FileButton, Group, Menu, Stack, Text, Textarea } from '@mantine/core'
import {
  IconArrowBackUp,
  IconArrowLeft,
  IconDots,
  IconEraser,
  IconLock,
  IconLogout,
  IconPhone,
  IconPhoto,
  IconRefresh,
  IconSend,
  IconShieldLock,
  IconTrash,
  IconUsers,
} from '@tabler/icons-react'
import type { KeyboardEvent } from 'react'
import { useMediaQuery } from '@mantine/hooks'
import { MembershipLine } from '../../components/chat/MembershipLine'
import { useNavigate, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { notifications } from '@mantine/notifications'
import { api } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { deserializeContent, serializeContent, type ChatContent, type ChatPhoto } from '../../lib/chatContent'
import { preparePhoto, type SealedPhoto } from '../../lib/photo'
import { PhotoGrid } from '../../components/chat/PhotoGrid'
import { ReplyQuote } from '../../components/chat/ReplyQuote'
import {
  MLS_APPLICATION,
  MEMBERSHIP,
  MLS_CONTROL_TYPES,
  parseMembership,
  MLS_DEVICE,
  PeerKeysMissingError,
  autoBackupSoon,
  base64ToBytes,
  catchUpToLatest,
  decryptChatMessage,
  confirmGroup,
  ensureGroup,
  mlsSession,
  primeGroup,
  removeGroupMember,
} from '../../lib/mls'
import {
  cacheContent,
  clearPreview,
  forgetBodies,
  loadCachedEntries,
  setPreview,
} from '../../lib/chatCache'
import {
  authenticated,
  isOwnMessage,
  resolveAuthor,
  type Attribution,
  type CachedEntry,
} from '../../lib/attribution'
import { forgetEnvelope, loadEnvelope, saveEnvelope } from '../../lib/chatEnvelope'
import { forgetPhotos } from '../../lib/photoCache'
import { forgetSeen } from '../../lib/lastSeen'
import { conversationAvatarKey, conversationTitle, otherMember } from '../../lib/conversation'
import { dayKey, groupByDay, messageTime } from '../../lib/time'
import { useAuth } from '../../auth/context'
import { useEventStream } from '../../hooks/useEventStream'
import { HISTORY_IMPORTED_EVENT } from '../../hooks/useHistorySync'
import { useChatScroll } from '../../hooks/useChatScroll'
import { usePrependAnchor } from '../../hooks/usePrependAnchor'
import { useEdgeSwipeBack } from '../../hooks/useEdgeSwipeBack'
import { applyReceipt, messageReceipt } from '../../lib/receipts'
import { MessageTicks } from '../../components/chat/MessageTicks'
import { ChannelAvatar } from '../../components/chat/ChannelAvatar'
import { DateSeparator } from '../../components/chat/DateSeparator'
import { CallEventBubble } from '../../components/chat/CallEventBubble'
import { CALL_EVENT, readCallEvent } from '../../lib/callEvent'
import { JumpToBottom } from '../../components/chat/JumpToBottom'
import { ChatSkeleton } from '../../components/chat/ChatSkeleton'
import { SafetyNumberModal } from '../../components/chat/SafetyNumber'
import { GroupMembersModal } from '../../components/chat/GroupMembersModal'
import { UserInfoModal } from '../../components/chat/UserInfoModal'
import { ConfirmModal } from '../../components/ConfirmModal'
import { useCalls } from '../../components/call/context'
import { useCallingAvailable } from '../../hooks/useCallingAvailable'
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
/** How many photos may ride on one message. Telegram's album is ten; so is this. */
const MAX_PHOTOS = 10

/**
 * How long a message that would not decrypt is given before the bubble calls it unreadable.
 *
 * A first decrypt can fail for a reason that fixes itself seconds later: the message is sealed to an
 * MLS epoch this device has not applied yet — a new member's first words ride in with the very Commit
 * that admits them — and the catch-up is already on its way. Saying "not available on this device"
 * in that gap is a lie that then corrects itself, which is worse than saying nothing.
 *
 * So: say nothing, and only call it unreadable once this much time has passed and it is still sealed.
 * Long enough for a settle to land, short enough that a genuinely unreadable message does not sit
 * there pretending to load.
 */
const SEALED_GRACE_MS = 6_000

/**
 * Reconciles the cached transcript with the server's newest page. Within the page's
 * time window the server is authoritative — a cached message the page does not include
 * was cleared or deleted (perhaps on another of this user's devices), so it is dropped.
 * Cached messages OLDER than the window are kept: they are history the page simply did
 * not reach, and the top-of-feed loader pages back to fill any gap between them. The
 * result is sorted oldest-first.
 */
/**
 * Unions two message lists by id and sorts oldest-first. Used by load-older: the cached transcript
 * can already hold messages the older page returns (the envelope cache shows up to 200, but the
 * load-older cursor tracks the newest page), so a blind prepend would duplicate them and shove the
 * feed. Merging by id drops the overlap and keeps everything ordered, while still pulling in the
 * genuinely-older messages — and the gap of messages that arrived while away — that the page adds.
 */
function mergeSorted(a: ChatMessage[], b: ChatMessage[]): ChatMessage[] {
  const byId = new Map<string, ChatMessage>()
  for (const m of a) byId.set(m.id, m)
  for (const m of b) byId.set(m.id, m)
  return [...byId.values()].sort((x, y) => x.createdAt.localeCompare(y.createdAt))
}

function reconcile(cached: ChatMessage[], fetched: ChatMessage[]): ChatMessage[] {
  // Only ever called with the newest page (no cursor), so an empty result is authoritative:
  // the whole history is gone — every message cleared (here or on another device) or deleted —
  // and the cached transcript must be dropped, not kept. Returning `cached` here would resurrect
  // exactly the messages a clear was meant to remove. A transient network failure never reaches
  // this function (it throws and the caller keeps the cache), so this is not that case.
  if (fetched.length === 0) return []
  const windowStart = fetched[0].createdAt // oldest of the newest page (fetched is oldest-first)
  const byId = new Map<string, ChatMessage>()
  for (const m of fetched) byId.set(m.id, m)
  for (const m of cached) {
    if (m.createdAt.localeCompare(windowStart) < 0) byId.set(m.id, m)
  }
  return [...byId.values()].sort((x, y) => x.createdAt.localeCompare(y.createdAt))
}

/**
 * Purges every local trace of a conversation on this device — decrypted bodies,
 * the message envelope, the list preview, and the read watermark. Called when the
 * conversation is deleted, cleared, or found gone on the server. The bodies cannot
 * be recovered afterwards (MLS keys are single-use); that is the intent.
 */
async function evictLocal(conversationId: string): Promise<void> {
  await Promise.all([forgetBodies(conversationId), forgetEnvelope(conversationId)])
  clearPreview(conversationId)
  forgetSeen(conversationId)
  forgetPhotos(conversationId)
}

export function ConversationChatRoute() {
  const { id = '' } = useParams()
  const { conversations, composerFocus } = useOutletContext<ChatOutletContext>()
  const { userId } = useAuth()
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const isMobile = useMediaQuery('(max-width: 48em)')
  const { call, place } = useCalls()
  const callingAvailable = useCallingAvailable()

  const [conversation, setConversation] = useState<Conversation | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([]) // oldest-first
  /**
   * The decrypted content of each message, by id — with THREE states, because two were not enough:
   *
   *   absent (undefined) — not tried yet. It has only just arrived and the decrypt is still running.
   *   null               — tried, and this device cannot read it. Permanent (MLS gives a device no
   *                        access to what was said before it joined).
   *   a ChatContent      — read.
   *
   * With only two, a message that had merely not been decrypted YET was indistinguishable from one
   * that never could be, so every arriving message flashed "Not available on this device" for the
   * instant before its body landed. The mobile app has always had the third state (contents is a
   * Map<String, ChatContent?>), which is why it never flashed.
   */
  // messageId -> what this device holds for it: the content AND how its author was established.
  // Attribution travels WITH the body deliberately — reading a message here and then asking the
  // envelope who wrote it is the hole this closes. See lib/attribution.
  const [bodies, setBodies] = useState<Record<string, CachedEntry | null>>({})
  // The conversation id whose local body cache has finished loading; message
  // processing waits for this so cached messages are never re-decrypted.
  const [readyId, setReadyId] = useState('')
  const [loading, setLoading] = useState(true)
  const [cursor, setCursor] = useState('')
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [unseen, setUnseen] = useState(0)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)

  /**
   * Photos picked but not yet sent — already downscaled, stripped of their metadata and sealed.
   *
   * Sealed at PICK time rather than at send time so the cost of encrypting a handful of megabytes is
   * paid while the user is still typing, not in the pause after they hit send.
   */
  const [pending, setPending] = useState<{ preview: string; sealed: SealedPhoto }[]>([])

  /** The message being replied to. */
  const [replyTo, setReplyTo] = useState<ChatMessage | null>(null)
  const [safetyOpen, setSafetyOpen] = useState(false)
  const [membersOpen, setMembersOpen] = useState(false)
  const [userInfoOpen, setUserInfoOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [confirmLeave, setConfirmLeave] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)
  const [actionBusy, setActionBusy] = useState(false)
  // The conversation whose peer has published no keys (so we cannot encrypt to them).
  // Keyed by id and derived, so switching chats clears the banner without a reset.
  const [peerNotReadyId, setPeerNotReadyId] = useState('')

  /**
   * Whether the group has been settled with the server for THIS conversation yet.
   *
   * Without it there is no way to tell "we have not asked" from "we asked, and this device is not in
   * the group" — and the app assumed the second. So every chat, every time it was opened, announced
   * that encryption was being set up until the network came back. A banner is only honest once the
   * answer is actually known.
   */
  /**
   * The conversation this tab can DECRYPT, from its own storage, with no network.
   *
   * Kept apart from groupId on purpose. Readability is safe to take from a cache; membership is not,
   * and the group to encrypt to is not. See primeGroup.
   */
  const [readableFor, setReadableFor] = useState('')
  const readable = readableFor === id

  const [settledFor, setSettledFor] = useState('')

  /**
   * Whether the group has been settled with the server for THIS conversation.
   *
   * Keyed on the id rather than reset in an effect, so switching chats cannot leave a stale "yes"
   * behind for a moment — which would be the same bug in the other direction.
   */
  const groupSettled = settledFor === id
  const peerNotReady = peerNotReadyId === id
  const textRef = useRef<HTMLTextAreaElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)

  const atBottomRef = useRef(true)
  // The decrypted-body cache mirrored into a ref, plus the set of message ids we
  // have already handled — so the one-shot decrypt never runs twice for a message.
  const bodiesRef = useRef<Record<string, CachedEntry | null>>({})
  const processedRef = useRef<Set<string>>(new Set())
  // Whether the network reconcile has landed for the current chat. The cache hydrate
  // and the network fetch race; if the network wins, the cache must not clobber its
  // (authoritative) result by re-showing messages the fetch deliberately dropped.
  const reconciledRef = useRef(false)
  // Messages waiting out SEALED_GRACE_MS before the bubble gives up on them, by id.
  const sealedTimers = useRef<Map<string, number>>(new Map())
  // The conversation currently on screen, for async work that may outlive a switch. Kept
  // current by the preload effect below, which is what resets the per-conversation refs anyway.
  const liveIdRef = useRef(id)

  /**
   * Starts a message's grace. When it runs out, the message is marked unreadable (null) — unless it
   * decrypted in the meantime, which is the whole point of waiting.
   *
   * Before giving up it looks at the DISK cache one last time. The plaintext can be there without
   * ever having reached this tab's state: another tab won the race for the message's one-shot key
   * and cached what it read, or a superseded pass of the decrypt effect did. Its write is
   * asynchronous, so even a pass that checked the cache can have looked a moment too early — this
   * is the look that cannot be too early.
   */
  const scheduleSealed = useCallback((messageId: string) => {
    if (sealedTimers.current.has(messageId)) return
    const timer = window.setTimeout(() => {
      sealedTimers.current.delete(messageId)
      // It arrived after all: an epoch caught up and the decrypt succeeded. Nothing to say.
      if (bodiesRef.current[messageId] != null) return
      void loadCachedEntries(id).then((cached) => {
        // The chat switched, or the body landed, while the cache was being read.
        if (liveIdRef.current !== id || bodiesRef.current[messageId] != null) return
        bodiesRef.current = { ...bodiesRef.current, [messageId]: cached[messageId] ?? null }
        setBodies(bodiesRef.current)
      })
    }, SEALED_GRACE_MS)
    sealedTimers.current.set(messageId, timer)
  }, [id])

  // Leaving the chat entirely: nothing is left to tell, and a timer that outlives the component
  // would setState on it.
  useEffect(() => {
    const timers = sealedTimers.current
    return () => {
      for (const timer of timers.values()) clearTimeout(timer)
      timers.clear()
    }
  }, [])
  // Messages that could not be read on the last pass — sealed to an MLS epoch this
  // device has not applied yet (a new member's first message rides in with the Commit
  // that admits them). They are parked here, not retired, and retried when the epoch
  // advances, so they decrypt in place instead of only after leaving and reopening.
  const failedRef = useRef<Set<string>>(new Set())
  // Bumped when a Welcome/Commit is applied while this chat is open; a change nudges
  // the decrypt effect to re-attempt the parked messages against the new epoch.
  const [catchUpTick, setCatchUpTick] = useState(0)
  const lastTickRef = useRef(0)
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

  // Reset the transcript the instant the open conversation changes. This is the
  // render-phase "adjust state when a prop changes" pattern React endorses (not an
  // effect), so the previous chat's messages never flash before this one's cache
  // loads: the reset lands in the same render as the id change, and the skeleton shows
  // until the preload effect below hydrates from cache or the network reconcile lands.
  const [shownId, setShownId] = useState(id)
  if (shownId !== id) {
    setShownId(id)
    setMessages([])
    setCursor('')
    setLoading(true)
  }

  const markNewestRead = useCallback(() => {
    const newest = messages[messages.length - 1]
    if (newest) conversations.markRead(id, newest.createdAt, newest.seq)
  }, [messages, id, conversations])

  const { scrollerRef, contentRef, atBottom, scrollToBottom } = useChatScroll(
    id,
    () => {
      setUnseen(0)
      markNewestRead()
    },
  )
  useEffect(() => {
    atBottomRef.current = atBottom
  })

  // Anchoring a load-older prepend so it does not jump the feed — synchronously, in the same commit,
  // rather than through the scroll hook's ResizeObserver. See usePrependAnchor.
  const prepend = usePrependAnchor(scrollerRef, messages)

  useEdgeSwipeBack(
    Boolean(isMobile),
    useCallback(() => navigate('/'), [navigate]),
  )

  useEffect(() => {
    if (!isMobile) textRef.current?.focus()
  }, [id, composerFocus, isMobile])

  // What this device already knows, from its own storage, before the network is asked anything.
  //
  // This is the difference between a chat that opens instantly and one that spends several round trips
  // telling the user encryption is being set up — on a device that has been holding the keys for weeks.
  // It was never setting anything up. It was waiting to be told a group id it already knew.
  //
  // Holding the ratchet is the proof, and it is local. Once we have it, messages decrypt and the
  // composer works; the settle below then runs in the background where nobody is looking.
  useEffect(() => {
    if (!userId) return
    let active = true

    // Priming makes the conversation READABLE. It deliberately does NOT set groupId — that is the
    // group `send` encrypts to, and a remembered id is not proof it is still the current one. If
    // another device reset the conversation, trusting the cache here would seal the next message to a
    // retired group that nobody is in, with no error anywhere. Only confirmGroup may set it.
    primeGroup(id)
      .then((primed) => {
        if (active && primed) setReadableFor(id)
      })
      .catch(() => {
        // Nothing known locally. The confirm below will say.
      })

    // The authoritative answer, in ONE round trip, alongside everything else the page is fetching. It
    // lands long before the user has typed anything, so the composer and the call button are honest by
    // the time they could be used.
    confirmGroup(id, userId)
      .then((gid) => {
        if (!active) return
        setGroupId(gid ?? '')
        setSettledFor(id)
      })
      .catch(() => {
        // Leave it unknown; the full settle gets another go.
      })

    return () => {
      active = false
    }
  }, [id, userId, setGroupId])

  // Fetch the conversation on open — and use the same round trip to confirm it still
  // exists. A 404 (getConversationMaybe returns null) means it was deleted, or this
  // device was removed, while we were away and offline enough to miss the live
  // `conversationDeleted` event. Treat it exactly as that event would: purge the local
  // caches and drop back to the list. A transient error (network, 500) is caught
  // separately and leaves the chat up, since it is not evidence the chat is gone.
  useEffect(() => {
    let active = true
    api
      .getConversationMaybe(id)
      .then((c) => {
        if (!active) return
        if (!c) {
          void evictLocal(id)
          conversations.refresh()
          navigate('/')
          return
        }
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
            setSettledFor(id)
            // The settle may have APPLIED COMMITS (catchUp runs inside it), and the first
            // decrypt pass has usually already run — at the epoch this device woke up with —
            // and parked whatever was sealed past it. Nothing else re-triggers those: the
            // group id string is unchanged, and the SSE tick only fires for a Commit arriving
            // LIVE. Without this nudge, a message sealed to an epoch we only just caught up
            // to stays "not available" on every open, forever, while being one decrypt away.
            setCatchUpTick((n) => n + 1)
          })
          .catch((e: unknown) => {
            // The other person has published no keys on any device, so nothing can be
            // encrypted to them. Say so plainly, where the user is about to type, rather
            // than letting them discover it by having a message fail to send.
            if (!active) return
            setPeerNotReadyId(e instanceof PeerKeysMissingError ? id : '')
            setSettledFor(id)
          })
      })
      .catch(() => active && setConversation(null))
    return () => {
      active = false
    }
    // navigate is stable; conversations.refresh is a stable useCallback. Re-running on
    // their identity would refetch needlessly, so they are intentionally omitted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, userId, setGroupId])

  // Keep asking, while this device is waiting to be let into the group.
  //
  // It has announced itself, and a member who holds the group will admit it — but only if one
  // of them has the app open. Retrying is what turns "nobody was around when I asked" into
  // "somebody will be, and I will notice". It is also what eventually establishes that nobody
  // is coming at all, which is the only way out of a group whose every holder has lost its
  // keys (see settleGroup).
  //
  // Stops the moment we are in.
  useEffect(() => {
    if (!userId || !conversation || conversation.id !== id || groupId) return
    let active = true
    const timer = setInterval(() => {
      ensureGroup(conversation, userId)
        .then((gid) => {
          if (!active || !gid) return
          setGroupId(gid)
          // Same as the on-open settle: being let in means Commits were just applied, and
          // the messages parked while we waited need one more look.
          setCatchUpTick((n) => n + 1)
        })
        .catch(() => {})
    }, 15_000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [id, userId, conversation, groupId, setGroupId])

  // Preload this conversation's already-decrypted bodies AND its cached message
  // envelope before any message processing — so cached messages are never
  // re-decrypted, and the transcript that was last on screen paints instantly from
  // disk instead of behind a skeleton while the network is asked for the newest page.
  //
  // The old chat's list is cleared first: without it, switching chats would flash the
  // previous conversation's messages until this resolves. Hydrating from cache then
  // replaces the skeleton with the real transcript in the same tick.
  useEffect(() => {
    let active = true
    liveIdRef.current = id
    bodiesRef.current = {}
    processedRef.current = new Set()
    failedRef.current = new Set()
    reconciledRef.current = false
    // Another chat's graces are not this one's: a timer left running would mark a message of the
    // conversation we just left, against a bodies map that no longer holds it.
    for (const timer of sealedTimers.current.values()) clearTimeout(timer)
    sealedTimers.current.clear()
    void Promise.all([loadCachedEntries(id), loadEnvelope(id)]).then(([cached, envelope]) => {
      if (!active) return
      bodiesRef.current = cached
      for (const messageId of Object.keys(cached)) processedRef.current.add(messageId)
      setBodies(cached)
      setReadyId(id)
      // Paint the cached transcript for an instant first frame — unless the network
      // reconcile already landed, in which case its authoritative result stands and the
      // cache must not re-show messages the server dropped (a clear on another device).
      if (envelope.length > 0 && !reconciledRef.current) {
        setMessages(envelope)
        // We have a transcript to show; the network fetch refines it in place rather
        // than gating first paint.
        setLoading(false)
      }
    })
    return () => {
      active = false
    }
  }, [id])

  // Device-to-device history sync just imported this conversation's past into the cache (a
  // co-member handed it over). Merge it into the on-screen bodies so it paints without a reopen:
  // messages that were hidden as unreadable now have content and appear. Only touches ids not
  // already resolved, so it never clobbers a live decrypt.
  useEffect(() => {
    const onImported = (e: Event) => {
      const detail = (e as CustomEvent<{ conversationId: string }>).detail
      if (!detail || detail.conversationId !== id) return
      void loadCachedEntries(id).then((cached) => {
        if (liveIdRef.current !== id) return
        let changed = false
        const next = { ...bodiesRef.current }
        for (const [messageId, content] of Object.entries(cached)) {
          if (next[messageId] == null) {
            next[messageId] = content
            processedRef.current.add(messageId)
            failedRef.current.delete(messageId)
            changed = true
          }
        }
        if (changed) {
          bodiesRef.current = next
          setBodies(next)
        }
      })
    }
    window.addEventListener(HISTORY_IMPORTED_EVENT, onImported)
    return () => window.removeEventListener(HISTORY_IMPORTED_EVENT, onImported)
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
    // Decryption needs only the groups this device can READ, which priming supplies from disk. It
    // must not wait for the server to confirm which one is current — that is a question about sending.
    if (readyId !== id || !userId || !conversation || (!groupId && !readable) || messages.length === 0)
      return
    const run = async () => {
      try {
        await mlsSession(userId)
      } catch {
        // This device has no keys yet and a backup is waiting to be restored (or the
        // WASM failed to load). Either way there is nothing to decrypt with; the
        // restore prompt is what resolves it.
        return
      }
      // A new epoch was applied since the last pass: un-park the messages that could
      // not be read before, so they get another attempt now the ratchet may reach them.
      if (catchUpTick !== lastTickRef.current) {
        lastTickRef.current = catchUpTick
        for (const mid of failedRef.current) processedRef.current.delete(mid)
        failedRef.current.clear()
      }
      // The disk cache, read at most once per pass. A message whose decrypt comes back empty may
      // still be readable from here: it is our own (a sender can never decrypt what it sent), it
      // predates this device joining the group, or another tab won the race for its one-shot key —
      // MLS lets exactly one succeed — and cached the plaintext for both of them.
      let disk: Record<string, CachedEntry> | null = null
      const fromDisk = async (messageId: string): Promise<CachedEntry | null> => {
        disk ??= await loadCachedEntries(id)
        return disk[messageId] ?? null
      }
      for (const m of messages) {
        // A membership note is written by the server in plaintext and has no key. Handing it to
        // the decrypt path would burn a lookup and mark it unreadable, which hides it.
        if (
          processedRef.current.has(m.id) ||
          MLS_CONTROL_TYPES.has(m.contentType) ||
          m.contentType === MEMBERSHIP
        )
          continue
        // Claimed BEFORE the decrypt, not after. This effect can overlap itself — any dep change
        // starts a new pass while an old one is still awaiting — and two passes reaching the same
        // message is not duplicated work but destruction: the first decrypt destroys the key, the
        // second comes back empty, and whichever result is kept had better be the full one.
        processedRef.current.add(m.id)
        let entry: CachedEntry | null
        try {
          if (m.contentType === CALL_EVENT || m.contentType === MLS_APPLICATION) {
            // Against whichever of the conversation's groups this message belongs to. A
            // conversation can have had more than one — if every device holding a group lost
            // its keys, the only way to talk again was to start a fresh one — and the old
            // groups are kept, so what was said to them is still readable here.
            //
            // The decrypt hands back the credential MLS AUTHENTICATED as the signer alongside the
            // bytes. That is the only trustworthy answer to who wrote this message, and it is
            // stored with the body so nothing downstream has to go back to the envelope for it.
            const opened = await decryptChatMessage(id, userId, m.ciphertext)
            const content = (opened && deserializeContent(opened.plaintext)) || null
            entry = content && opened ? { content, attribution: authenticated(opened.sender) } : null
            if (entry) {
              void cacheContent(id, m.id, entry.content, entry.attribution)
              // A call's record is encrypted exactly like a message, because it IS one — the
              // difference is only what the body means, which the bubble decides. The one thing
              // it must not do is become the chat list's preview: "{"outcome":"missed"}" is not
              // a sentence, and a missed call has no words to preview. A photo with no caption,
              // though, still has to say something there — an empty row reads as a bug rather
              // than as a picture.
              if (m.contentType === MLS_APPLICATION) {
                setPreview(
                  id,
                  entry.content.body || (entry.content.photos?.length ? '__photo__' : ''),
                  m.id,
                  // The list's copy of the authenticated sender. Without it the sidebar would be
                  // back to asking the server whether the newest message is yours.
                  entry.attribution.kind === 'legacy' ? '' : entry.attribution.userId,
                )
              }
            } else {
              // Empty-handed, not thrown: decryptChatMessage never throws for a message it
              // cannot read. This is the path an already-consumed key comes back on, so this —
              // not the catch below — is where the cached plaintext rescues it.
              entry = await fromDisk(m.id)
            }
          } else {
            // Legacy plaintext (pre-encryption messages on the wire). There is no MLS signature
            // over these and there never was, so the envelope is genuinely all there is — the
            // compatibility fallback, never presented as verified.
            const content = deserializeContent(base64ToBytes(m.ciphertext)) || null
            entry = content ? { content, attribution: { kind: 'legacy' } } : null
          }
        } catch {
          // Something on the way to decrypting failed (the session died mid-pass). The cache may
          // still hold the body. Never retry the decrypt itself.
          entry = await fromDisk(m.id)
        }
        // The chat switched under this pass. Nothing decrypted is lost — it was cached above —
        // but it must not be written into another conversation's state.
        if (liveIdRef.current !== id) return
        if (entry) {
          // Written through immediately, not batched to the end of the pass. A pass being
          // superseded is routine (a receipt landing re-runs the effect), and a decrypted body
          // dropped on the floor can never be decrypted again — only the disk cache would hold
          // it, and only a reload would look there.
          bodiesRef.current = { ...bodiesRef.current, [m.id]: entry }
          setBodies(bodiesRef.current)
          failedRef.current.delete(m.id)
          // Now cache-only (the MLS key is spent); keep the backup current so a new device
          // inherits it. No-op unless the user has unlocked backup this session.
          autoBackupSoon(userId)
        } else {
          // Parked, not retired: it is in the processed set so ordinary re-renders skip it, but
          // recorded in failedRef so the next epoch catch-up releases it for another try — a new
          // member's first message "just appears" instead of needing a reopen. The bubble stays
          // quiet while the grace runs, because the catch-up that would make this readable is
          // very often already in flight; the grace timer also gives the disk cache a last look
          // before calling it unreadable. See scheduleSealed / SEALED_GRACE_MS.
          failedRef.current.add(m.id)
          scheduleSealed(m.id)
        }
      }
    }
    void run()
    // scheduleSealed only changes with the conversation id, which is already a dep — listing it
    // costs no extra runs.
  }, [messages, readyId, userId, id, conversation, groupId, readable, catchUpTick, scheduleSealed])

  // Reconcile the cached transcript with the server's newest page. `loading` is not
  // forced true here — the preload effect above owns the skeleton and has already
  // cleared it when the cache had something to show, so a warm open never flashes one.
  // The fetched page is UNION-merged with whatever is on screen (cache or a prior
  // fetch), keyed by immutable id, so nothing already shown disappears and new
  // messages slot into place. A transient failure leaves the cached transcript up.
  useEffect(() => {
    let active = true
    const run = async () => {
      try {
        const page = await api.listChatMessages(id, '', PAGE_SIZE)
        if (!active) return
        const fetched = page.messages.slice().reverse()
        // Mark reconciled before the state update so a cache hydrate resolving in the
        // same tick defers to this result rather than unioning dropped messages back.
        reconciledRef.current = true
        setMessages((prev) => {
          const base = prev.length > 0 && prev[0].conversationId === id ? prev : []
          return reconcile(base, fetched)
        })
        setCursor(page.nextCursor)
        // The whole history is gone (cleared here or elsewhere). Drop the on-disk envelope too —
        // the save effect skips empty writes, so without this the stale file would survive and
        // re-flash the cleared transcript on every future open.
        if (fetched.length === 0) void forgetEnvelope(id)
        const newest = page.messages[0]
        if (newest) conversations.markRead(id, newest.createdAt, newest.seq)
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

  // Persist the on-screen transcript's newest window whenever it settles — after the
  // reconcile above, pagination, a live arrival, or a send — so the next open of this
  // chat paints from it. Guarded on the conversation id so a mid-switch render, where
  // `messages` still holds the previous chat for a tick, cannot write it under the new
  // id's key.
  useEffect(() => {
    if (messages.length === 0 || messages[0].conversationId !== id) return
    void saveEnvelope(id, messages)
  }, [messages, id])

  async function loadOlder() {
    // prepend.pending stays true from a prior load-older until its restore has run, so a fast second
    // scroll-to-top cannot start here and clobber the pending anchor.
    if (!cursor || loadingOlder || prepend.pending.current) return
    setLoadingOlder(true)
    try {
      const page = await api.listChatMessages(id, cursor, PAGE_SIZE, true)
      const older = page.messages.slice().reverse()
      // Capture the anchor right before the prepend; the layout effect restores it before paint.
      prepend.beforePrepend()
      // Merge, not prepend: the older page can overlap what the envelope cache already shows, and a
      // blind prepend would duplicate those messages and jump the feed. See mergeSorted.
      setMessages((prev) => mergeSorted(prev, older))
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
    // Someone got further through the conversation: move their watermark, and our own ticks with
    // it. Patched onto the FETCHED conversation because that is what the header and the ticks read
    // once it has loaded — the list's copy (patched in useConversationList) is only the fallback.
    if (e.receipt) {
      const receipt = e.receipt
      setConversation((prev) =>
        prev && prev.id === id ? { ...prev, members: applyReceipt(prev.members, receipt) } : prev,
      )
      return
    }
    if (!e.chatMessage) return
    const msg = e.chatMessage

    // MLS protocol traffic. It is not a message, and it never reaches the transcript —
    // but it is how the group changes shape, so it has to be acted on the moment it
    // arrives rather than the next time somebody reloads.
    if (MLS_CONTROL_TYPES.has(msg.contentType)) {
      // Only the user is needed to catch up — requiring the fetched conversation too meant a
      // Commit arriving in the first moments of an open was silently dropped.
      if (!userId) return
      if (msg.contentType === MLS_DEVICE) {
        // Handled app-wide by useDeviceAdmission, which listens whether or not this chat is
        // open — a device waiting to be let in must not depend on somebody happening to be
        // looking at the right conversation.
        return
      }
      // A Welcome or a Commit: catch up on it DIRECTLY — apply it, or join from it if it is
      // the Welcome that admits this device — rather than through ensureGroup. ensureGroup
      // dedupes concurrent callers onto one in-flight settle, so a Commit that arrived while
      // a settle was already running rode a catch-up that had finished before the Commit
      // existed: the tick bumped, the retry ran against the old epoch, and a message sealed
      // to the new one stayed parked forever. It also re-ran full device reconciliation on
      // every Commit, which is how two clients fed each other's reconcile loops into a
      // commit storm. An incoming Commit calls for catching up and confirming, nothing more;
      // admitting OTHER devices stays with useDeviceAdmission and the on-open settle.
      void catchUpToLatest(id, userId)
        .then(() => confirmGroup(id, userId))
        .then((gid) => {
          setGroupId(gid ?? '')
          setSettledFor(id)
          // The epoch advanced (a member added, a device admitted). Nudge the decrypt
          // effect so a message sealed to the new epoch — a newly joined user's first
          // words — is read now, not only after leaving and coming back.
          setCatchUpTick((n) => n + 1)
        })
        .catch(() => {})
      return
    }

    setMessages((prev) => (prev.some((m) => m.id === msg.id) ? prev : [...prev, msg]))
    if (atBottomRef.current) {
      conversations.markRead(id, msg.createdAt, msg.seq)
    } else {
      setUnseen((n) => n + 1)
    }
  })

  async function reloadConversation() {
    const c = await api.getConversation(id)
    setConversation(c)
  }

  // A manual pull of the conversation's current state — its metadata and its newest
  // page — for when the live stream has missed something and the chat looks stale.
  // Re-attempting decryption (catchUpTick) picks up anything that was still sealed.
  async function refreshChat() {
    try {
      const [c, page] = await Promise.all([
        api.getConversation(id),
        api.listChatMessages(id, '', PAGE_SIZE),
      ])
      setConversation(c)
      setMessages(page.messages.slice().reverse())
      setCursor(page.nextCursor)
      setCatchUpTick((n) => n + 1)
    } catch (e) {
      notifyError(t('dashboard.loadFailed'), e)
    }
  }

  async function deleteChat() {
    setActionBusy(true)
    try {
      await api.deleteConversation(id)
      await evictLocal(id)
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
      await evictLocal(id)
      navigate('/')
    } catch (e) {
      notifyError(t('group.actionFailed'), e)
      setActionBusy(false)
    }
  }

  // Clear the conversation's history: purge it server-side (the ciphertext, which no
  // one can read anyway) and wipe the local plaintext caches, keeping the conversation
  // itself. Then empty the on-screen transcript in place so the now-clear chat shows
  // without a reload. The bodies are gone for good — MLS keys are single-use — which is
  // exactly what "clear history" has to mean.
  async function clearHistory() {
    setActionBusy(true)
    try {
      await api.clearChatHistory(id)
      await evictLocal(id)
      processedRef.current = new Set()
      failedRef.current = new Set()
      bodiesRef.current = {}
      setBodies({})
      setMessages([])
      setCursor('')
      setConfirmClear(false)
      conversations.refresh()
    } catch (e) {
      notifyError(t('group.actionFailed'), e)
    } finally {
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

  /**
   * The user id a message is attributed to — from MLS wherever this device has read the message,
   * from the envelope only when it has not (and so has no signature to go on either).
   */
  function authorOf(message: { id: string; senderId: string }): ReturnType<typeof resolveAuthor> {
    return resolveAuthor(bodies[message.id]?.attribution ?? { kind: 'legacy' }, message.senderId)
  }

  /**
   * Who wrote the quoted message. Undefined when we do not hold it.
   *
   * Named from the AUTHENTICATED sender of the quoted message wherever this device has decrypted
   * it. A quote is a claim about what somebody else said, so attributing it from the envelope —
   * the server's word — would let the server put words in a member's mouth in the one place a
   * reader is least likely to check them against the original.
   */
  function quoteAuthor(messageId: string): string | undefined {
    const quoted = messages.find((m) => m.id === messageId)
    if (!quoted) return undefined
    const author = authorOf(quoted)
    // The cryptography and the envelope disagree about this message. Naming either one would be
    // picking a side; ReplyQuote renders its "unknown author" form instead.
    if (author.tampered) return undefined
    const member = header?.members.find((mem) => mem.userId === author.userId)?.user
    return member?.displayName || member?.username || undefined
  }

  /**
   * The quoted text, rendered from what THIS DEVICE holds.
   *
   * Undefined when it holds nothing — the quoted message predates this browser joining the group, so
   * it can never be decrypted here. ReplyQuote says so rather than showing a blank.
   */
  function quoteText(messageId: string): string | undefined {
    const quoted = bodies[messageId]?.content
    if (!quoted) return undefined
    if (quoted.body) return quoted.body
    return quoted.photos?.length ? t('chat.photo') : ''
  }

  /** Seals a picked photo up front, so the encryption cost is paid while the user is still typing. */
  async function attach(files: File[]) {
    const room = MAX_PHOTOS - pending.length
    const taken = files.slice(0, room)

    for (const file of taken) {
      try {
        const sealed = await preparePhoto(file)
        setPending((prev) => [
          ...prev,
          { preview: URL.createObjectURL(file), sealed },
        ])
      } catch {
        notifications.show({ color: 'red', message: t('chat.photoFailed') })
      }
    }
  }

  function removePending(index: number) {
    setPending((prev) => {
      URL.revokeObjectURL(prev[index].preview)
      return prev.filter((_, i) => i !== index)
    })
  }

  // A photo with no caption is a perfectly good message, so an empty box must not disable the
  // button when a picture is attached.
  const canSend = draft.trim().length > 0 || pending.length > 0

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

      // The photos go up FIRST, each under a fresh key. The message is what carries those keys, so a
      // message posted before its photos exist would reference blobs nobody can fetch — and if an
      // upload fails, the message is simply never sent, which beats a bubble with a permanent hole.
      const photos: ChatPhoto[] = []
      for (const item of pending) {
        const blobId = await api.uploadAttachment(id, item.sealed.bytes)
        photos.push({
          id: blobId,
          key: item.sealed.key,
          w: item.sealed.width,
          h: item.sealed.height,
          mime: item.sealed.mime,
          size: item.sealed.size,
        })
      }

      const content: ChatContent = {
        body,
        ...(replyTo ? { replyTo: replyTo.id } : {}),
        ...(photos.length ? { photos } : {}),
      }

      const ciphertext = await session.encrypt(gid, serializeContent(content))
      const msg = await api.sendChatMessage(id, ciphertext, MLS_APPLICATION)

      // We can never decrypt our own MLS message, so record its plaintext now and
      // mark it handled so the SSE echo does not try (and fail) to decrypt it.
      //
      // Attributed to this device's own credential — the same identity MLS authenticates for
      // every other member reading it. We wrote it, so this is not a claim about a sender, it is
      // the sender.
      const mine: Attribution = authenticated(session.identity)
      processedRef.current.add(msg.id)
      bodiesRef.current = { ...bodiesRef.current, [msg.id]: { content, attribution: mine } }
      setBodies(bodiesRef.current)
      void cacheContent(id, msg.id, content, mine)
      setPreview(
        id,
        body || (photos.length ? '__photo__' : ''),
        msg.id,
        mine.kind === 'legacy' ? '' : mine.userId,
      )
      // A new message this device will never decrypt again lives only in the cache now —
      // keep the backup current so it survives to a new device. No-op unless backup is
      // unlocked this session.
      autoBackupSoon(userId)

      setDraft('')
      setPending([])
      setReplyTo(null)
      setMessages((prev) => (prev.some((m) => m.id === msg.id) ? prev : [...prev, msg]))
      // Re-stick to the bottom now, before the bubble has laid out. Waiting a frame and then
      // animating there raced the feed's own re-pin — the two fought over the same scrollTop —
      // and left the new message parked under the keyboard often enough to look broken. Taking
      // the position immediately means the feed simply holds it as the bubble grows into place.
      scrollToBottom()
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

  // What actually renders in the feed. Two kinds are dropped:
  //   * MLS control messages, which carry no user-visible text; and
  //   * messages this device cannot read (body resolved to null) — a stretch of history
  //     from before this device joined, or one not in the backup it restored. Rather than
  //     announce "not available on this device", we simply do not show them: a new device
  //     opens a clean conversation of what it CAN read, not a wall of apologies. A message
  //     still decrypting (body undefined) is kept — its bubble shows "…" until it lands.
  const visibleMessages = messages.filter(
    (m) =>
      !MLS_CONTROL_TYPES.has(m.contentType) &&
      // A system line has no body to decrypt and must not be dropped for lacking one.
      (m.contentType === MEMBERSHIP || bodies[m.id] !== null),
  )

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
          {/* Tapping the avatar opens who this chat is with: a group's roster, or a
              direct peer's contact card. */}
          <ChannelAvatar
            id={avatarKey}
            name={title || '·'}
            avatarId={avatarId}
            size={38}
            label={t('chat.openInfo')}
            onClick={() => (isGroup ? setMembersOpen(true) : setUserInfoOpen(true))}
          />
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
          {/* Calling is 1:1 only, and only once this device is actually in the group — there is
              nothing to encrypt a call to otherwise. Hidden entirely on a server that has no
              STUN/TURN configured: a button that can only ever fail is worse than no button. */}
          {!isGroup && callingAvailable && (
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label={t('call.start')}
              data-testid="start-call"
              disabled={!groupId || call !== null}
              onClick={() => void place(id)}
            >
              <IconPhone size={20} />
            </ActionIcon>
          )}
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
              <Menu.Item
                leftSection={<IconRefresh size={18} />}
                onClick={() => void refreshChat()}
              >
                {t('chat.refresh')}
              </Menu.Item>
              {isGroup && (
                <Menu.Item
                  leftSection={<IconUsers size={18} />}
                  onClick={() => setMembersOpen(true)}
                >
                  {t('group.membersTitle')}
                </Menu.Item>
              )}
              <Menu.Item
                leftSection={<IconEraser size={18} />}
                onClick={() => setConfirmClear(true)}
              >
                {t('chat.clearHistory')}
              </Menu.Item>
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

      {header && !isGroup && (
        <UserInfoModal
          user={otherMember(header, userId ?? '') ?? null}
          opened={userInfoOpen}
          onClose={() => setUserInfoOpen(false)}
        />
      )}

      <ConfirmModal
        opened={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={deleteChat}
        loading={actionBusy}
        title={isGroup ? t('group.deleteGroup') : t('chat.deleteChat')}
      >
        <Text size="sm">
          {isGroup ? t('group.deleteGroupConfirm') : t('chat.deleteChatConfirm')}
        </Text>
      </ConfirmModal>

      <ConfirmModal
        opened={confirmClear}
        onClose={() => setConfirmClear(false)}
        onConfirm={clearHistory}
        loading={actionBusy}
        title={t('chat.clearHistory')}
        confirmLabel={t('chat.clearHistory')}
      >
        <Text size="sm">{t('chat.clearHistoryConfirm')}</Text>
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
              groupByDay(visibleMessages).map((day) => (
                // One section per day, so the day's sticky pill pins within its own day and is
                // carried off by it. See groupByDay.
                <section className="pheme-day" key={dayKey(day[0].createdAt)}>
                  <DateSeparator iso={day[0].createdAt} />
                  {day.map((m) => {
                    const entry = bodies[m.id]
                    const content = entry?.content
                    // Who MLS says wrote this, and whether the envelope agrees.
                    //
                    // `own` decides which side of the feed the bubble sits on, and it is answered
                    // from the AUTHENTICATED sender for every message this device has read. Left to
                    // the envelope only where there is no plaintext — and therefore no signature —
                    // to go on, which is a message this device cannot read at all.
                    const author = resolveAuthor(entry?.attribution ?? { kind: 'legacy' }, m.senderId)
                    const own = isOwnMessage(entry?.attribution, m.senderId, userId ?? '')
                    const senderName =
                      isGroup && !author.tampered
                        ? conversation?.members.find((mem) => mem.userId === author.userId)?.user
                        : undefined
                    // Somebody joined or left: a line the conversation says about itself, centred
                    // and quiet, with no bubble and no sender — because nobody sent it.
                    if (m.contentType === MEMBERSHIP) {
                      const membership = parseMembership(base64ToBytes(m.ciphertext))
                      if (!membership) return null
                      return (
                        <MembershipLine
                          key={m.id}
                          event={membership}
                          members={conversation?.members ?? []}
                          myUserId={userId ?? ''}
                        />
                      )
                    }
                    const event =
                      m.contentType === CALL_EVENT && content != null
                        ? readCallEvent(content.body)
                        : null
                    if (event) {
                      return (
                        <CallEventBubble
                          key={m.id}
                          event={event}
                          own={own}
                          at={messageTime(m.createdAt, i18n.language)}
                        />
                      )
                    }
                    return (
                      <div
                        key={m.id}
                        className="pheme-bubble pheme-chat-bubble"
                        data-own={own}
                        data-testid="chat-message"
                      >
                        {isGroup && !own && senderName && (
                          <Text size="xs" fw={600} c="iris">
                            {senderName.displayName || senderName.username || 'User'}
                          </Text>
                        )}

                        {/*
                          The signature and the envelope name DIFFERENT people.
                          Nothing here picks one of them. MLS authenticated a leaf, the server
                          claims somebody else posted it, and the honest rendering of that is to
                          say so — attributing it either way is exactly the silent misattribution
                          the authenticated sender exists to prevent.
                        */}
                        {author.tampered && (
                          <Text size="xs" fw={600} c="red" data-testid="sender-mismatch">
                            {t('chat.senderMismatch')}
                          </Text>
                        )}

                        {/* Context first, then the reply — the way a reply reads. */}
                        {content?.replyTo && (
                          <ReplyQuote
                            author={quoteAuthor(content.replyTo)}
                            text={quoteText(content.replyTo)}
                          />
                        )}

                        {content?.photos?.length ? (
                          <PhotoGrid conversationId={id} photos={content.photos} />
                        ) : null}

                        {content === undefined ? (
                          // Not tried yet — it has just arrived and its decrypt is still running. An
                          // ellipsis is the honest thing HERE, where something really is about to
                          // appear; saying "not available" for the instant before a body lands is
                          // what made every incoming message flash that it could not be read.
                          <Text size="sm" c="dimmed" aria-hidden>
                            …
                          </Text>
                        ) : content === null ? (
                          // Unreadable — this device cannot read it and never will. Filtered out
                          // of visibleMessages before it reaches here, so this branch renders
                          // nothing; it stays only to keep the type exhaustive.
                          null
                        ) : content.body ? (
                          // A photo with no caption has no body line at all: an empty <Text> still
                          // takes a row of leading and leaves a strip of dead space under the picture.
                          <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>
                            {content.body}
                          </Text>
                        ) : null}
                        <div className="pheme-bubble-footer">
                          {/* Reply is the only action the server can support: there are no reactions,
                              and a sealed message cannot be edited or deleted. It appears on hover
                              rather than sitting there permanently, so a wall of text stays a wall of
                              text. */}
                          {/* Only on a message we can actually read: there is nothing to quote of one
                              that is still decrypting, or that this device will never open. */}
                          {content != null && (
                            <ActionIcon
                              className="pheme-bubble-reply"
                              aria-label={t('chat.reply')}
                              variant="subtle"
                              color="gray"
                              size="sm"
                              onClick={() => setReplyTo(m)}
                            >
                              <IconArrowBackUp size={14} />
                            </ActionIcon>
                          )}
                          <Text size="xs" c="dimmed">
                            {messageTime(m.createdAt, i18n.language)}
                          </Text>
                          {/* Ticks on our own messages only — see MessageTicks. */}
                          {own && (
                            <MessageTicks
                              receipt={messageReceipt(m.seq ?? 0, header?.members ?? [], userId ?? '')}
                            />
                          )}
                        </div>
                      </div>
                    )
                  })}
                </section>
              ))}
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
        {/* Only once we KNOW the answer is no. `settled` is what separates that from "still asking",
            and while we are still asking the honest thing to do is say nothing. */}
        {!peerNotReady && !loading && groupSettled && !groupId && (
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
        {/* What you are replying to, above the box you are typing into — so the context is where the
            eye already is. */}
        {replyTo && (
          <Group gap="xs" wrap="nowrap" mb="xs">
            <div style={{ flex: 1, minWidth: 0 }}>
              <ReplyQuote
                author={quoteAuthor(replyTo.id)}
                text={quoteText(replyTo.id)}
                compact
              />
            </div>
            <CloseButton
              aria-label={t('common.cancel')}
              onClick={() => setReplyTo(null)}
            />
          </Group>
        )}

        {pending.length > 0 && (
          <div className="pheme-attachments">
            {pending.map((item, i) => (
              <div className="pheme-attachment" key={item.preview}>
                <img src={item.preview} alt="" />
                <CloseButton
                  size="xs"
                  radius="xl"
                  variant="filled"
                  color="dark"
                  aria-label={t('common.delete')}
                  onClick={() => removePending(i)}
                />
              </div>
            ))}
          </div>
        )}

        <Group gap="xs" align="flex-start" wrap="nowrap">
          <FileButton
            multiple
            accept="image/*"
            onChange={(files) => void attach(files)}
          >
            {(props) => (
              <ActionIcon
                {...props}
                aria-label={t('chat.attachPhoto')}
                variant="subtle"
                color="gray"
                size="lg"
                disabled={sending || pending.length >= MAX_PHOTOS}
              >
                <IconPhoto size={20} />
              </ActionIcon>
            )}
          </FileButton>
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
