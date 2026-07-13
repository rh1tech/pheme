// Trust-on-first-use pinning for conversation safety numbers.
//
// The safety number is derived from the keys actually in the MLS group, so it
// changes if the (untrusted) server ever substitutes a key — the signature of a
// machine-in-the-middle. Comparing it out of band is the strong check, but most
// people never will. Pinning it on first sight and shouting when it changes is the
// check that costs the user nothing, and it is what turns a silent compromise into
// a visible one.
//
// A number legitimately changes when a member is added or removed, or when someone
// reinstalls and gets new keys. So a change is a prompt to re-verify, not proof of
// an attack — the UI says exactly that.

const pinKey = (conversationId: string) => `pheme.safety.${conversationId}`

export type SafetyState =
  | { status: 'first-seen'; number: string }
  | { status: 'unchanged'; number: string }
  | { status: 'changed'; number: string; previous: string }

/**
 * Compares a conversation's current safety number against the pinned one, pinning
 * it if this is the first sight. Does not auto-accept a change: the caller decides
 * what to show, and `accept` records the new number once the user has re-verified.
 */
export function checkSafetyNumber(conversationId: string, current: string): SafetyState {
  const previous = read(conversationId)
  if (!previous) {
    write(conversationId, current)
    return { status: 'first-seen', number: current }
  }
  if (previous === current) return { status: 'unchanged', number: current }
  return { status: 'changed', number: current, previous }
}

/** Pins the current number, after the user has re-verified a change. */
export function acceptSafetyNumber(conversationId: string, current: string): void {
  write(conversationId, current)
}

function read(conversationId: string): string {
  try {
    return localStorage.getItem(pinKey(conversationId)) ?? ''
  } catch {
    return ''
  }
}

function write(conversationId: string, value: string): void {
  try {
    localStorage.setItem(pinKey(conversationId), value)
  } catch {
    // Storage blocked: we simply cannot pin. The out-of-band check still works.
  }
}

/** Drops every pin. Called on logout with the rest of this account's state. */
export function clearSafetyPins(): void {
  try {
    const stale = Object.keys(localStorage).filter((k) => k.startsWith('pheme.safety.'))
    for (const key of stale) localStorage.removeItem(key)
  } catch {
    // Nothing pinned to clear.
  }
}
