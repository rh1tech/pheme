import { useEffect, useState } from 'react'
import { api } from '../lib/api'

/**
 * Whether this server has calling configured at all.
 *
 * A deployment without STUN/TURN cannot place a call that works for anyone behind a NAT, and
 * the ICE endpoint says so outright rather than handing back an empty list. There is no point
 * showing a call button that can only ever produce an error, so we ask once and hide it.
 *
 * Asked once per session, not per conversation: the answer is a property of the server.
 */
let known: boolean | null = null
let asking: Promise<boolean> | null = null

export function useCallingAvailable(): boolean {
  // Seeded from the cached answer, so a second conversation does not re-ask and does not
  // flicker the button in on the next render.
  const [available, setAvailable] = useState(known ?? false)

  useEffect(() => {
    if (known !== null) return
    let active = true
    asking ??= api
      .iceServers()
      .then(() => true)
      .catch(() => false)
      .then((ok) => {
        known = ok
        return ok
      })
    void asking.then((ok) => active && setAvailable(ok))
    return () => {
      active = false
    }
  }, [])

  return available
}
