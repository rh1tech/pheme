import { useEffect, useState } from 'react'
import { BUILD_ID, fetchDeployedBuildId, isOutdated } from '../lib/version'

/**
 * How often a tab asks whether it is still current.
 *
 * Generous on purpose. Nothing here is urgent — the tab keeps working, it is just behind — and this
 * runs for the whole life of every open tab, so the cost of asking is paid forever.
 */
const POLL_MS = 10 * 60 * 1000

/**
 * Whether this tab is running code older than what is deployed.
 *
 * Latches: once true it stays true and the polling stops. The answer cannot un-become true (a tab
 * cannot catch up without reloading, which is the whole point), so there is nothing left to ask.
 *
 * Checks on returning to the tab as well as on a timer, because that is when it matters and when a
 * backgrounded timer is least trustworthy: the answer should be ready by the time someone who left
 * this open overnight starts typing.
 */
export function useAppOutdated(): boolean {
  const [outdated, setOutdated] = useState(false)

  useEffect(() => {
    if (outdated) return
    let cancelled = false

    const check = async () => {
      if (cancelled || document.visibilityState !== 'visible') return
      const deployed = await fetchDeployedBuildId()
      if (!cancelled && isOutdated(deployed, BUILD_ID)) setOutdated(true)
    }

    void check()
    const timer = window.setInterval(() => void check(), POLL_MS)
    const onVisible = () => void check()
    document.addEventListener('visibilitychange', onVisible)

    return () => {
      cancelled = true
      clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [outdated])

  return outdated
}
