// Web Push registration helpers: register the service worker, subscribe with the
// server's VAPID public key, and produce a PushSubscription JSON string suitable
// for the Pheme device registry.

import { api } from './api'

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

  const registration = await navigator.serviceWorker.register('/sw.js')
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
  })
  return device.id
}
