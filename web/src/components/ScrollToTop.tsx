import { useEffect } from 'react'
import { useLocation } from 'react-router-dom'

/**
 * Resets the window scroll position to the top on every route change, so a new
 * screen never inherits the previous one's scroll offset. Renders nothing.
 */
export function ScrollToTop() {
  const { pathname } = useLocation()
  useEffect(() => {
    window.scrollTo({ top: 0, left: 0, behavior: 'instant' })
  }, [pathname])
  return null
}
