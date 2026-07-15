// A small, bounded cache of DECRYPTED photo object URLs, keyed by conversation + blob id.
//
// Without it, every time a photo's <img> remounts — a day section scrolling back into view after an
// older page loads, or simply reopening the chat — the ciphertext is re-fetched and re-decrypted, and
// the picture blinks from empty to loaded, tugging the scroll. The blob and its key never change, so
// the decrypted bytes are identical every time; they are cached here and reused instantly.
//
// It is bounded and LRU: only the most-recently-seen photos are kept, and an evicted URL is revoked so
// its plaintext does not sit in memory for the life of the tab. That is a deliberate, limited
// relaxation of the old "revoke on every unmount" rule — bounded exposure (the last MAX_CACHED photos)
// in exchange for a feed that does not flicker. On delete/clear/logout the conversation's entries are
// dropped outright (forgetPhotos), same as its other caches.

import { api } from './api'
import { openPhoto } from './photo'

// How many decrypted photos to hold at once. Large enough that ordinary scrolling never evicts a photo
// still on screen, small enough that the plaintext held in memory stays bounded.
const MAX_CACHED = 60

const cache = new Map<string, string>() // key -> objectURL; Map iteration order is insertion order (LRU)
const inflight = new Map<string, Promise<string>>() // dedupe concurrent loads of the same photo

const keyOf = (conversationId: string, photoId: string) => `${conversationId}:${photoId}`

/** Records a URL as most-recently-used, evicting (and revoking) the oldest past the cap. */
function touch(key: string, url: string): void {
  cache.delete(key)
  cache.set(key, url)
  while (cache.size > MAX_CACHED) {
    const oldest = cache.keys().next().value
    if (oldest === undefined) break
    const stale = cache.get(oldest)
    cache.delete(oldest)
    if (stale) URL.revokeObjectURL(stale)
  }
}

/**
 * The object URL for a decrypted photo — from cache when it has been seen, otherwise fetched, decrypted
 * and cached. Takes the blob's identifying fields (not the whole ChatPhoto) so callers can depend on
 * just those. Concurrent calls for the same photo share one in-flight fetch. The returned URL is owned
 * by the cache; callers must NOT revoke it (eviction and forgetPhotos do that).
 */
export async function loadPhotoUrl(
  conversationId: string,
  photoId: string,
  photoKey: string,
  mime: string,
): Promise<string> {
  const key = keyOf(conversationId, photoId)

  const cached = cache.get(key)
  if (cached) {
    touch(key, cached)
    return cached
  }

  const existing = inflight.get(key)
  if (existing) return existing

  const promise = (async () => {
    const sealed = await api.attachmentBytes(conversationId, photoId)
    const bytes = await openPhoto(photoKey, sealed)
    const url = URL.createObjectURL(new Blob([bytes as BlobPart], { type: mime }))
    touch(key, url)
    return url
  })().finally(() => inflight.delete(key))

  inflight.set(key, promise)
  return promise
}

/** Drops one conversation's cached photo URLs — on delete, clear-history, or a 404. */
export function forgetPhotos(conversationId: string): void {
  const prefix = `${conversationId}:`
  for (const [key, url] of cache) {
    if (!key.startsWith(prefix)) continue
    cache.delete(key)
    URL.revokeObjectURL(url)
  }
}
