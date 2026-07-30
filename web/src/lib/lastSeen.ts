// Per-channel read state, kept client-side.
//
// The API has no read-state model, and adding one would mean a new collection
// plus a write on every channel open. The chat list only needs to know whether a
// channel has anything newer than what this browser last displayed, which a
// timestamp per channel answers exactly. It is intentionally per-browser: an
// unread dot that does not sync across devices is a smaller lie than a count
// that cannot be computed.

const STORAGE_KEY = 'pheme.lastSeen.v1'

type SeenMap = Record<string, string>

function read(): SeenMap {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return {}
    return parsed as SeenMap
  } catch {
    // Private-mode browsers and corrupt entries both land here. An empty map
    // marks everything read, which is the quieter failure.
    return {}
  }
}

/** The ISO timestamp of the newest message this browser has displayed per channel. */
export function loadLastSeen(): Readonly<SeenMap> {
  return read()
}

/** Records that the channel has been read up to the given message timestamp. */
export function markSeen(channelId: string, iso: string): void {
  const current = read()
  if ((current[channelId] ?? '') >= iso) return
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...current, [channelId]: iso }))
  } catch {
    // Storage full or blocked: the dot stays lit. Harmless.
  }
}

/** Clears all read-state on logout so the next user cannot see channel membership. */
export function clearAllSeen(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Blocked storage: harmless, key will be overwritten on next write.
  }
}

/** Drops a channel's read state (e.g. after leaving or deleting it). */
export function forgetSeen(channelId: string): void {
  const current = read()
  if (!(channelId in current)) return
  const rest = Object.fromEntries(Object.entries(current).filter(([id]) => id !== channelId))
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(rest))
  } catch {
    // Storage full or blocked: a stale entry for a gone channel is harmless.
  }
}
