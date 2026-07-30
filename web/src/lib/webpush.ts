// Web Push registration helpers: register the service worker, subscribe with the
// server's VAPID public key, and produce a PushSubscription JSON string suitable
// for the Pheme device registry.

import { api } from './api'
import { saveWebDeviceId, loadMlsDeviceId } from './device'
import { SERVICE_WORKER_URL } from './sw'

/** Reports whether the browser supports Web Push. */
export function webPushSupported(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

/** Reports whether the current device is iOS or iPadOS. */
export function isIOS(): boolean {
  const ua = navigator.userAgent || ''
  const iOSUA = /iPad|iPhone|iPod/.test(ua)
  // iPadOS 13+ reports as a Mac; detect it by touch support.
  const iPadOS = navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1
  return iOSUA || iPadOS
}

/** Reports whether the app is running as an installed (standalone) PWA. */
export function isStandalonePWA(): boolean {
  return (
    window.matchMedia?.('(display-mode: standalone)').matches === true ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true
  )
}

/** Why Web Push can't be used here, or 'supported' when it can. */
export type WebPushAvailability = 'supported' | 'ios-needs-install' | 'unsupported'

/**
 * Explains whether Web Push is usable on this device. iOS only exposes the Push
 * APIs inside an installed Home Screen PWA, so a normal Safari tab reports
 * 'ios-needs-install' rather than a hard 'unsupported'.
 */
export function webPushAvailability(): WebPushAvailability {
  if (webPushSupported()) return 'supported'
  if (isIOS() && !isStandalonePWA()) return 'ios-needs-install'
  return 'unsupported'
}

export interface WebPushState {
  supported: boolean
  permission: NotificationPermission | 'unsupported'
  /** True when the browser has an active push subscription. */
  subscribed: boolean
}

/**
 * Inspects the browser's current Web Push state without prompting: whether it is
 * supported, the notification permission, and whether a live PushSubscription
 * already exists.
 */
export async function getWebPushState(): Promise<WebPushState> {
  if (!webPushSupported()) {
    return { supported: false, permission: 'unsupported', subscribed: false }
  }
  let subscribed = false
  try {
    const registration = await navigator.serviceWorker.getRegistration()
    if (registration) {
      subscribed = (await registration.pushManager.getSubscription()) !== null
    }
  } catch {
    // ignore — treat as not subscribed
  }
  return { supported: true, permission: Notification.permission, subscribed }
}

function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4)
  const normalized = (base64 + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(normalized)
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

/**
 * Reports whether an existing subscription was created with the given VAPID
 * public key. A subscription bound to a different applicationServerKey must be
 * recreated, otherwise the push service rejects deliveries (e.g. Apple returns
 * 403 BadJwtToken).
 */
function subscriptionMatchesKey(sub: PushSubscription, vapidPublicKey: string): boolean {
  const existing = sub.options?.applicationServerKey
  if (!existing) return false
  const a = new Uint8Array(existing)
  const b = urlBase64ToUint8Array(vapidPublicKey)
  if (a.byteLength !== b.byteLength) return false
  for (let i = 0; i < a.byteLength; i++) {
    if (a[i] !== b[i]) return false
  }
  return true
}

/**
 * Re-registers a browser whose push is ALREADY on, so its device record catches up with what this
 * build can do.
 *
 * A device record is not just an address, it is a statement of capability — the server withholds
 * message previews from any device that has not said it can decrypt one, because sending a
 * data-only push to a client that cannot render it shows the user nothing at all.
 *
 * That gate had no way to ever open on web. `registerWebPushDevice` was only reachable from the
 * enable-notifications banner, and the banner renders nothing once notifications are on — so every
 * browser that had already enabled them kept whatever capability it declared the day it first
 * subscribed, forever. Previews were shipped and silently withheld from everyone who had been
 * using the app the longest.
 *
 * Safe to call on every load: the subscription is reused rather than recreated, and the server
 * upserts the device by its push endpoint rather than making a second one. Once per page load
 * regardless, because two components render the banner.
 */
let syncStarted = false

/** Resets the sync flag so a newly logged-in user triggers a fresh registration. */
export function resetWebPushSync(): void {
  syncStarted = false
}

export async function syncWebPushDevice(): Promise<void> {
  if (syncStarted) return
  syncStarted = true
  try {
    const state = await getWebPushState()
    // Only for a browser that is already subscribed. This must never be the thing that ASKS for
    // permission — that is the banner's job, on a deliberate click.
    if (!state.supported || state.permission !== 'granted' || !state.subscribed) return
    saveWebDeviceId(await registerWebPushDevice())
  } catch {
    // Nothing here is worth interrupting a page load for. The cost of failing is that previews
    // stay off for this browser until the next attempt.
  }
}

/**
 * Subscribes the browser to Web Push and registers it as a Pheme web device.
 * Returns the created device ID, or throws if permission is denied or the server
 * has no VAPID key configured.
 */
export async function registerWebPushDevice(): Promise<string> {
  if (!webPushSupported()) throw new Error('Web Push is not supported in this browser')

  const { vapidPublicKey } = await api.meta()
  if (!vapidPublicKey) throw new Error('server has no VAPID public key configured')

  const permission = await Notification.requestPermission()
  if (permission !== 'granted') throw new Error('notification permission denied')

  const registration = await navigator.serviceWorker.register(SERVICE_WORKER_URL)
  await navigator.serviceWorker.ready

  // Reuse an existing subscription only if it was created with the current
  // server VAPID key; otherwise the push service rejects deliveries, so drop
  // the stale subscription and create a fresh one.
  let subscription = await registration.pushManager.getSubscription()
  if (subscription && !subscriptionMatchesKey(subscription, vapidPublicKey)) {
    await subscription.unsubscribe()
    subscription = null
  }
  if (!subscription) {
    subscription = await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(vapidPublicKey) as BufferSource,
    })
  }

  const device = await api.createDevice({
    platform: 'web',
    webPushSub: JSON.stringify(subscription),
    // This build ships a service worker that can decrypt a message and draw the notification
    // itself, so say so — the server withholds previews from devices that do not. Safe to assert
    // unconditionally here: the worker is deployed with this bundle, and it degrades to the
    // server's generic text on its own if a decrypt does not land.
    canRenderPreview: true,
    // Ties this push address to this browser's MLS device, so that revoking the device can find
    // and delete it. Without the link the two registries share no field at all, and a revoked
    // browser keeps its subscription — and keeps being sent the ciphertext of messages it is no
    // longer allowed to read.
    mlsDeviceId: loadMlsDeviceId() ?? undefined,
  })
  return device.id
}
