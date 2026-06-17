// Stores the locally-registered Web Push device ID so the user can subscribe it
// to channels without re-registering.

const KEY = 'pheme.webDeviceId'

export function saveWebDeviceId(id: string): void {
  localStorage.setItem(KEY, id)
}

export function loadWebDeviceId(): string | null {
  return localStorage.getItem(KEY)
}
