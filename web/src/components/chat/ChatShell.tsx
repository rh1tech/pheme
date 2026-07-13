import { useCallback, useEffect, useMemo, useState } from 'react'
import { Outlet, useMatch } from 'react-router-dom'
import { ChatSidebar } from './ChatSidebar'
import { useChannelList } from '../../hooks/useChannelList'
import { useVisualViewportHeight } from '../../hooks/useVisualViewport'
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
  const list = useChannelList()
  const match = useMatch({ path: '/channels/:id', end: false })
  const activeId = match?.params.id
  const viewportHeight = useVisualViewportHeight()

  // Picking a channel should put the cursor in its message box. Counted rather
  // than derived from the channel id, so re-picking the channel already open —
  // which changes no id — still focuses.
  const [composerFocus, setComposerFocus] = useState(0)
  const onSelectChannel = useCallback(() => setComposerFocus((n) => n + 1), [])
  const context = useMemo<ChatOutletContext>(
    () => ({ list, composerFocus }),
    [list, composerFocus],
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

  return (
    <div
      className="pheme-shell"
      data-view={activeId ? 'chat' : 'list'}
      style={
        viewportHeight === null
          ? undefined
          : ({ '--pheme-viewport-h': `${viewportHeight}px` } as React.CSSProperties)
      }
    >
      <ChatSidebar list={list} activeId={activeId} onSelectChannel={onSelectChannel} />
      <div className="pheme-main">
        <Outlet context={context} />
      </div>
    </div>
  )
}
