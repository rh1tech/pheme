import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import type { PublicUser } from '../lib/types'

/** A query must be at least this long before the server is asked — one letter matches everyone. */
const MIN_QUERY = 2
const DEBOUNCE_MS = 250

export interface UserSearchState {
  results: PublicUser[]
  searching: boolean
  /** True once the query is long enough to have been sent. */
  active: boolean
}

/**
 * Debounced public-user search. Returns matches for the trimmed query once it is
 * at least two characters, and clears them the moment it is shorter — so stale
 * results never linger under a query that no longer asks for them.
 *
 * @param exclude ids to drop from the results (e.g. people already in a group)
 */
export function useUserSearch(query: string, exclude?: ReadonlySet<string>): UserSearchState {
  const [results, setResults] = useState<PublicUser[]>([])
  const [searching, setSearching] = useState(false)

  const trimmed = query.trim()
  const active = trimmed.length >= MIN_QUERY

  useEffect(() => {
    // Too short to search: run nothing. The cleared results are DERIVED at return time
    // rather than set here, so the effect never calls setState synchronously.
    if (!active) return
    let live = true
    const handle = window.setTimeout(() => {
      setSearching(true)
      api
        .searchUsers(trimmed)
        .then((users) => {
          if (!live) return
          setResults(exclude ? users.filter((u) => !exclude.has(u.id)) : users)
        })
        .catch(() => live && setResults([]))
        .finally(() => live && setSearching(false))
    }, DEBOUNCE_MS)
    return () => {
      live = false
      window.clearTimeout(handle)
    }
  }, [trimmed, active, exclude])

  // Below the minimum length there is nothing to show and nothing in flight, whatever
  // the last long query left in state.
  return { results: active ? results : [], searching: active && searching, active }
}
