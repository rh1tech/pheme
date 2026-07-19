/**
 * Who belongs in an encrypted group, and who does not.
 *
 * Pulled out of mls.ts so it can be tested directly. It was reachable only through a session that
 * needs WASM, a server and a real group, so the rules that decide WHO CAN READ A CONVERSATION were
 * exercised only end to end — and one of them (a revoked device keeping its leaf) shipped broken.
 *
 * Everything here is pure: strings in, strings out, no session, no network. That is the point.
 */

/** A leaf identity: `userId:deviceId`. */
export function deviceIdentity(userId: string, deviceId: string): string {
  return `${userId}:${deviceId}`
}

/** The device half of a credential identity, or '' for a legacy identity that has no device. */
export function deviceOf(identity: string): string {
  const sep = identity.indexOf(':')
  return sep === -1 ? '' : identity.slice(sep + 1)
}

/** The user half of a credential identity, or '' for a legacy identity. */
export function userOf(identity: string): string {
  const sep = identity.indexOf(':')
  return sep === -1 ? '' : identity.slice(0, sep)
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

    if (leaf.indexOf(':') === -1) {
      out.push(leaf) // legacy identity: nobody can hold its keys
      continue
    }

    const userId = userOf(leaf)
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
