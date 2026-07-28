import { api } from './api'

/**
 * Whether the server says THIS device has been revoked.
 *
 * Answers only when it is sure. A network failure, an expired session, a malformed reply — anything
 * other than a clear "your id is in the revoked list" — returns false, so the identity is kept. The
 * asymmetry is deliberate: wrongly keeping a revoked identity costs one more failed send, and
 * wrongly discarding a live one destroys the keys to every conversation this device can read, on a
 * launch that merely happened to be offline.
 *
 * Absence from the LIVE list is not enough on its own and is not used: a device missing from it may
 * equally never have registered, and that case must register rather than start over.
 *
 * The registry lookup is a parameter so a test can hand it a fake — including one that throws —
 * without mocking a module that pulls in the whole API client to answer a question about one array.
 */
export async function revokedLocally(
  deviceId: string,
  fetchRegistry: () => Promise<{ revoked: string[] }> = api.myDeviceRegistry,
): Promise<boolean> {
  if (deviceId === '') return false
  try {
    const { revoked } = await fetchRegistry()
    return revoked.includes(deviceId)
  } catch {
    return false
  }
}
