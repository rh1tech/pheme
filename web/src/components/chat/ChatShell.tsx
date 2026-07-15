import { useCallback, useEffect, useMemo, useState } from 'react'
import { Outlet, useMatch } from 'react-router-dom'
import { useMediaQuery } from '@mantine/hooks'
import { ChatSidebar } from './ChatSidebar'
import { KeyRestoreGate } from './KeyBackup'
import { CallProvider } from '../call/CallProvider'
import { CallUI } from '../call/CallUI'
import { mlsSession } from '../../lib/mls'
import { useAuth } from '../../auth/context'
import { useChannelList } from '../../hooks/useChannelList'
import { useConversationList } from '../../hooks/useConversationList'
import { useDeviceAdmission } from '../../hooks/useDeviceAdmission'
import { useColdLaunchToList } from '../../hooks/useColdLaunchToList'
import { useKeyboardOpen, useVisualViewportHeight } from '../../hooks/useVisualViewport'
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
  const channelMatch = useMatch({ path: '/channels/:id', end: false })
  const chatMatch = useMatch({ path: '/chats/:id', end: false })
  const activeId = channelMatch?.params.id ?? chatMatch?.params.id
  const viewportHeight = useVisualViewportHeight()
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
          viewportHeight === null
            ? undefined
            : ({
                '--pheme-viewport-h': `${viewportHeight}px`,
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
        <CallUI />
      </div>
    </CallProvider>
  )
}
