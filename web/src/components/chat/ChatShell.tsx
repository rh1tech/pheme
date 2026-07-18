import { useCallback, useEffect, useMemo, useState } from 'react'
import { Outlet, useMatch } from 'react-router-dom'
import { useMediaQuery } from '@mantine/hooks'
import { ChatSidebar } from './ChatSidebar'
import { KeyRestoreGate, RecoveryCodeGate } from './KeyBackup'
import { CallProvider } from '../call/CallProvider'
import { CallUI } from '../call/CallUI'
import { mlsSession } from '../../lib/mls'
import { useAuth } from '../../auth/context'
import { useChannelList } from '../../hooks/useChannelList'
import { useConversationList } from '../../hooks/useConversationList'
import { useDeviceAdmission } from '../../hooks/useDeviceAdmission'
import { useHistorySync } from '../../hooks/useHistorySync'
import { useActiveChatSync } from '../../hooks/useActiveChatSync'
import { useColdLaunchToList } from '../../hooks/useColdLaunchToList'
import { useKeyboardOpen, useVisualViewportRect } from '../../hooks/useVisualViewport'
import type { ChatOutletContext } from './context'
import './chat.css'

/**
 * The chat surface: the channel list beside the conversation, mounted once for
 * `/` and every `/channels/*` route so the list's data and scroll position
 * survive switching channels.
 *
 * The channel list is passed down through the Outlet context rather than
 * refetched by the conversation, so a live message updates the list and the feed
 * from the same stream event.
 */
export function ChatShell() {
  const { userId } = useAuth()
  const list = useChannelList()
  const conversations = useConversationList()
  // Let other people's newly signed-in devices into the groups they belong to, from anywhere
  // in the app — not only from the conversation they concern, which nobody may have open.
  useDeviceAdmission(userId)
  // Answer a newly-joined device's request for the history it holds none of, and receive our own.
  useHistorySync(userId)
  const channelMatch = useMatch({ path: '/channels/:id', end: false })
  const chatMatch = useMatch({ path: '/chats/:id', end: false })
  const activeId = channelMatch?.params.id ?? chatMatch?.params.id
  // Tell the service worker what is on screen, so it does not notify about a message the reader is
  // already looking at. It cannot read this off the client's url — see useActiveChatSync.
  useActiveChatSync(activeId)
  // Pin the shell to the VISUAL viewport — its top (offsetTop) and its height —
  // rather than sizing by height alone at the document's top. iOS moves the layout
  // viewport out from under a fixed-height root when the keyboard opens; sizing by
  // height without following offsetTop left the shell's bottom short of the keyboard,
  // so the composer floated above it. This is the same anchoring the bottom sheets use.
  const viewport = useVisualViewportRect()
  const keyboardOpen = useKeyboardOpen()
  // Read synchronously on the first render (not deferred to an effect): the cold-launch
  // redirect below decides once, at mount, and a first-render "false" that only flips to
  // true afterwards would make it miss the very launch it exists to catch.
  const isMobile = useMediaQuery('(max-width: 48em)', undefined, {
    getInitialValueInEffect: false,
  })

  // On a phone, a cold launch lands on the list, not whatever channel the last
  // session was left viewing (which iOS restores). A notification tap is exempt.
  useColdLaunchToList(Boolean(isMobile))

  // Picking a channel should put the cursor in its message box. Counted rather
  // than derived from the channel id, so re-picking the channel already open —
  // which changes no id — still focuses.
  const [composerFocus, setComposerFocus] = useState(0)
  const onSelectChannel = useCallback(() => setComposerFocus((n) => n + 1), [])
  const context = useMemo<ChatOutletContext>(
    () => ({ list, conversations, composerFocus }),
    [list, conversations, composerFocus],
  )

  // The shell is a fixed-height app, not a scrolling document. Marking the root
  // lets styles.css stop the page itself from scrolling for this surface only —
  // the container-layout pages (profile, admin) still scroll normally.
  useEffect(() => {
    document.documentElement.dataset.surface = 'chat'
    return () => {
      delete document.documentElement.dataset.surface
    }
  }, [])

  // Bring up this device's encryption identity as soon as the user reaches the chat
  // surface, rather than waiting until they open a conversation.
  //
  // Creating the session is what publishes this device's KeyPackages, and a user with
  // none published cannot be added to a group — so anyone trying to start a chat with
  // them fails. Deferring it to the first conversation they open means a brand-new
  // user is unreachable until they happen to open one, which is exactly backwards.
  useEffect(() => {
    if (!userId) return
    mlsSession(userId).catch(() => {
      // No keys yet and a backup is waiting: KeyRestoreGate below prompts for it.
    })
  }, [userId])

  return (
    // The call layer wraps the whole surface, not a conversation: a call outlives the chat it
    // was placed from, and an incoming call has to ring even when nothing is open.
    <CallProvider>
      <div
        className="pheme-shell"
        data-view={activeId ? 'chat' : 'list'}
        data-keyboard={keyboardOpen ? 'open' : undefined}
        style={
          // ONLY while the keyboard is up. The rest of the time the height comes from CSS
          // (`100dvh`), and that is the point.
          //
          // This used to be pinned to visualViewport.height unconditionally, and the failure mode
          // was severe: the height is a JavaScript-held pixel value, so ANY missed viewport update
          // left the app rendering into part of the screen with page background under it. iOS drops
          // those updates — around the keyboard, on resume from background — and once the number was
          // stale nothing recomputed it. A third of an iPhone screen, until the app was relaunched.
          //
          // `100dvh` cannot go stale. It is the browser's own measurement, re-evaluated as the
          // dynamic toolbar comes and goes, and it needs no listener to stay right. The keyboard is
          // the one case it does NOT cover on iOS — Safari does not honour interactive-widget, so
          // the keyboard overlays the layout viewport rather than shrinking it, and only
          // visualViewport sees it. So that is the only case JS is trusted for.
          //
          // The blast radius of a stale value is now bounded by how long the keyboard is open, and
          // it self-corrects the moment the keyboard closes.
          !keyboardOpen || viewport === null
            ? undefined
            : ({
                position: 'fixed',
                top: `${viewport.offsetTop}px`,
                left: 0,
                right: 0,
                height: `${viewport.height}px`,
                // The mobile info sheet still sizes to this.
                '--pheme-viewport-h': `${viewport.height}px`,
              } as React.CSSProperties)
        }
      >
        <ChatSidebar
          list={list}
          conversations={conversations}
          activeId={activeId}
          onSelectChannel={onSelectChannel}
        />
        <div className="pheme-main">
          <Outlet context={context} />
        </div>
        <KeyRestoreGate />
        <RecoveryCodeGate />
        <CallUI />
      </div>
    </CallProvider>
  )
}
