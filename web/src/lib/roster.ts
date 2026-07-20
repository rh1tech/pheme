/**
 * Who belongs in an encrypted group, and who does not.
 *
 * Pulled out of mls.ts so it can be tested directly. It was reachable only through a session that
 * needs WASM, a server and a real group, so the rules that decide WHO CAN READ A CONVERSATION were
 * exercised only end to end — and one of them (a revoked device keeping its leaf) shipped broken.
 *
 * Everything here is pure: strings in, strings out, no session, no network. That is the point.
 */

// The home domain this client's own credentials are qualified under. It comes
// from the server (GET /v1/meta), so every client on a host agrees on it, and
// is set once at startup before any MLS work. A domain-qualified credential is
// what makes a member on one host distinct from a same-named member on another.
let homeDomainValue = 'local'

/** Sets the home domain used to build THIS client's own credential identities. */
export function setHomeDomain(domain: string): void {
  if (domain) homeDomainValue = domain
}

/** The home domain in effect. */
export function homeDomain(): string {
  return homeDomainValue
}

/**
 * A leaf identity: `mimi://<domain>/d/<userId>/<deviceId>`, matching the bytes
 * the pheme-mls crate puts in a credential.
 *
 * The domain defaults to this client's home domain — the only identities this
 * function builds are for the local client's own devices. A REMOTE member's
 * identity is never built here; it is read from their credential and parsed by
 * userOf/deviceOf, which take whatever domain the credential actually carries.
 */
export function deviceIdentity(userId: string, deviceId: string, domain = homeDomainValue): string {
  return `mimi://${domain}/d/${userId}/${deviceId}`
}

// Parses `mimi://<domain>/d/<user>/<device>` into its parts, or null if it is
// not that form.
function parseIdentity(identity: string): { domain: string; user: string; device: string } | null {
  const rest = identity.startsWith('mimi://') ? identity.slice('mimi://'.length) : ''
  if (!rest) return null
  const parts = rest.split('/')
  // domain / "d" / user / device
  if (parts.length !== 4 || parts[1] !== 'd') return null
  return { domain: parts[0], user: parts[2], device: parts[3] }
}

/** The device half of a credential identity, or '' if it does not parse. */
export function deviceOf(identity: string): string {
  return parseIdentity(identity)?.device ?? ''
}

/**
 * The bare user id of a credential — the host-local user segment. Returns '' for
 * an identity that does not parse (a legacy `user:device` leaf, whose keys no
 * one holds).
 *
 * Deliberately BARE, not domain-qualified: the roster compares this against the
 * server's membership and key-package directory, both keyed by the host-local
 * user id. Distinctness across hosts is carried by the full leaf identity (which
 * includes the domain), not by this. `userKey` builds the qualified form for the
 * one place that needs it — a removal target the crate matches against a
 * credential.
 */
export function userOf(identity: string): string {
  return parseIdentity(identity)?.user ?? ''
}

/** The domain a credential is under, or '' if it does not parse. */
export function domainOf(identity: string): string {
  return parseIdentity(identity)?.domain ?? ''
}

/**
 * The qualified user key `mimi://<domain>/u/<user>` — the form the pheme-mls
 * crate's `user_of` returns, and therefore the form a removal target must take
 * to match a member's credential. Defaults to this client's home domain.
 */
export function userKey(userId: string, domain = homeDomainValue): string {
  return `mimi://${domain}/u/${userId}`
}

/**
 * The leaves that should not be in the group any more.
 *
 * Four reasons, and the ORDER of the checks is load-bearing:
 *
 *  1. Never ourselves. Pruning our own leaf removes our own ability to read.
 *  2. A LEGACY leaf, from before leaves carried a device id. No current client can hold its keys,
 *     so it can never read anything and never leaves on its own.
 *  3. A departed member — they are not on the roster any more.
 *  4. A REVOKED device, which the server names explicitly. This must be checked BEFORE the
 *     "cannot tell" bail below: terminating a device deletes its KeyPackages, so a revoked device
 *     has none published and would otherwise be waved through — which is exactly how a device
 *     whose access had just been removed kept its leaf, and with it everything sent afterwards.
 *  5. A ghost device: the member is here, they have published devices, and this is not one of
 *     them.
 *
 * A member with NO published devices is deliberately left alone. That is somebody who has never
 * opened the app, not somebody to evict.
 */
export function staleLeaves(
  selfIdentity: string,
  leaves: readonly string[],
  memberIds: readonly string[],
  published: Readonly<Record<string, string[]>>,
  revoked: Readonly<Record<string, string[]>> = {},
): string[] {
  const members = new Set(memberIds)
  const out: string[] = []

  for (const leaf of leaves) {
    if (leaf === selfIdentity) continue // never prune ourselves

    const userId = userOf(leaf)
    if (userId === '') {
      out.push(leaf) // unparseable / legacy identity: nobody can hold its keys
      continue
    }

    if (!members.has(userId)) {
      out.push(leaf) // departed member
      continue
    }

    if ((revoked[userId] ?? []).includes(deviceOf(leaf))) {
      out.push(leaf) // revoked device, said so by the server
      continue
    }

    const devices = published[userId] ?? []
    if (devices.length === 0) continue // cannot tell; leave them be
    if (!devices.includes(deviceOf(leaf))) out.push(leaf) // ghost device
  }

  return out
}

/**
 * The published devices that are not yet leaves of the group and should be added.
 *
 * Never our own — it holds the group, or is creating it, and claiming our own KeyPackage would
 * burn one for nothing. Never a known zombie: a device whose published package produced a leaf that
 * does not answer to its own identity, which if re-claimed forever is half of an add/prune war that
 * once burned five hundred epochs in one conversation.
 */
export function missingDevices(
  selfIdentity: string,
  published: Readonly<Record<string, string[]>>,
  leaves: readonly string[],
  zombies: ReadonlySet<string> = new Set(),
): { userId: string; deviceId: string }[] {
  const have = new Set(leaves)
  const out: { userId: string; deviceId: string }[] = []

  for (const [userId, deviceIds] of Object.entries(published)) {
    for (const deviceId of deviceIds) {
      const identity = deviceIdentity(userId, deviceId)
      if (identity === selfIdentity) continue
      if (have.has(identity)) continue
      if (zombies.has(identity)) continue
      out.push({ userId, deviceId })
    }
  }

  return out
}
