import { useCallback, useEffect, useState } from 'react'

/**
 * A simple seconds countdown. Returns the remaining seconds and a `start`
 * function to (re)arm it. Used for the "resend code" cooldown so the button
 * stays disabled until the per-email send window elapses.
 */
export function useCountdown(): [number, (seconds: number) => void] {
  const [remaining, setRemaining] = useState(0)

  const start = useCallback((seconds: number) => setRemaining(seconds), [])

  useEffect(() => {
    if (remaining <= 0) return
    const id = window.setTimeout(() => setRemaining((s) => s - 1), 1000)
    return () => window.clearTimeout(id)
  }, [remaining])

  return [remaining, start]
}
