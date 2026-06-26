import { useEffect } from 'react'
import { useLocation } from 'react-router-dom'

// Disable the browser's automatic scroll restoration so back/forward navigation
// doesn't re-apply the previous screen's scroll position after our reset.
if (typeof history !== 'undefined' && 'scrollRestoration' in history) {
  history.scrollRestoration = 'manual'
}

function resetScroll() {
  // The page may scroll on the window, the document root, or the Mantine
  // AppShell main element depending on the layout — reset whichever applies.
  window.scrollTo(0, 0)
  document.documentElement.scrollTop = 0
  document.body.scrollTop = 0
  document.querySelectorAll('.mantine-AppShell-main, [data-scroll-reset]').forEach((el) => {
    ;(el as HTMLElement).scrollTop = 0
  })
}

/**
 * Scrolls back to the top on every route change so a new screen never inherits
 * the previous screen's scroll position. Runs after paint (double rAF) so the
 * new route's content is laid out before we reset. Renders nothing.
 */
export function ScrollToTop() {
  const { pathname } = useLocation()
  useEffect(() => {
    resetScroll()
    const raf = requestAnimationFrame(() => requestAnimationFrame(resetScroll))
    return () => cancelAnimationFrame(raf)
  }, [pathname])
  return null
}
