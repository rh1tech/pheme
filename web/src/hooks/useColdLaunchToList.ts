import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'

// The path the app was loaded at, captured ONCE at module evaluation — before React
// mounts or any in-app navigation happens. This is the URL the browser launched with,
// which on iOS is whatever the last session was left on: relaunching a Safari tab, or a
// home-screen web app, restores the last-viewed channel rather than honouring the
// manifest's "/" start_url. That is what makes the app reopen on a channel.
const launchPath = typeof window !== 'undefined' ? window.location.pathname : '/'
const launchSearch = typeof window !== 'undefined' ? window.location.search : ''

// Runs at most once per page load: only a cold launch should be redirected, never a
// later in-app navigation to a channel.
let handled = false

/**
 * Whether the app is running as an installed, standalone home-screen app rather than
 * inside a normal browser tab. `navigator.standalone` is the iOS Safari signal;
 * `display-mode: standalone` covers the others.
 */
function isStandalone(): boolean {
  if (typeof window === 'undefined') return false
  const iosStandalone = (window.navigator as { standalone?: boolean }).standalone === true
  return iosStandalone || window.matchMedia?.('(display-mode: standalone)').matches === true
}

/**
 * On a cold launch of the installed app on a phone, land on the conversation list
 * rather than whatever channel or chat the last session happened to be viewing.
 *
 * An installed iOS app has no address bar and restores its last URL on relaunch, so a
 * user who was last reading a channel reopens straight into it — and mobile is
 * single-pane, so the list they need is nowhere in sight and there is no URL to hint
 * where they are. The list is the right home.
 *
 * Deliberately limited to the standalone app. In a normal browser tab the URL bar shows
 * where you are and a shared link to a specific channel must open that channel, so the
 * restore is not a trap and must not be second-guessed. Desktop keeps the URL too — the
 * list is always visible there. A notification tap is the one standalone exception: it
 * appends `?from=push` (see the service worker) and its deep link is honoured.
 */
export function useColdLaunchToList(isMobile: boolean): void {
  const navigate = useNavigate()

  useEffect(() => {
    if (handled) return
    handled = true

    if (!isMobile || !isStandalone()) return
    const fromPush = new URLSearchParams(launchSearch).get('from') === 'push'
    const deepLaunch = launchPath.startsWith('/channels/') || launchPath.startsWith('/chats/')
    if (deepLaunch && !fromPush) navigate('/', { replace: true })
  }, [isMobile, navigate])
}
