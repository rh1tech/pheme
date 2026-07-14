// The two device ids this app keeps, which are NOT the same thing.
//
// They used to share one localStorage key, and they mean entirely different things:
//
//   * The WEB PUSH device id is a server-issued `Device.ID` (POST /v1/devices). It names
//     a push target, and channel subscriptions are keyed by it.
//   * The MLS device id is a client-minted UUID naming this device's leaf in every
//     encrypted group (`userId:deviceId`). The server never issues it.
//
// Sharing a key meant whichever subsystem wrote last won. MLS writes on every session
// load — it re-derives the id from its own credential, which is authoritative — so in
// practice MLS quietly overwrote the push id, and the push code then looked up a device
// that did not exist. Keep them apart.

const PUSH_KEY = 'pheme.webDeviceId'
const MLS_KEY = 'pheme.mlsDeviceId'

/** The server-issued Web Push device id (`Device.ID`). */
export function saveWebDeviceId(id: string): void {
  localStorage.setItem(PUSH_KEY, id)
}

export function loadWebDeviceId(): string | null {
  return localStorage.getItem(PUSH_KEY)
}

export function clearWebDeviceId(): void {
  localStorage.removeItem(PUSH_KEY)
}

/** This device's MLS id — its leaf in every encrypted group. */
export function saveMlsDeviceId(id: string): void {
  localStorage.setItem(MLS_KEY, id)

  // Undo the damage the shared key did. If the push slot holds OUR id, it is a value we
  // overwrote back when both lived in one key — so the push code is currently looking up
  // a device that does not exist. Clear it, and it re-registers itself (the server upserts
  // a web device by its push endpoint, so nothing is lost server-side).
  //
  // Only clear it when it is exactly our id: any other value is a real push device id and
  // must be left alone.
  if (localStorage.getItem(PUSH_KEY) === id) localStorage.removeItem(PUSH_KEY)
}

export function loadMlsDeviceId(): string | null {
  return localStorage.getItem(MLS_KEY)
}
