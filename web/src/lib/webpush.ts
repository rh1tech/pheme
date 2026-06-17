// Web Push registration helpers: register the service worker, subscribe with the
// server's VAPID public key, and produce a PushSubscription JSON string suitable
// for the Pheme device registry.

import { api } from './api'

/** Reports whether the browser supports Web Push. */
export function webPushSupported(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
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

  const existing = await registration.pushManager.getSubscription()
  const subscription =
    existing ??
    (await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(vapidPublicKey) as BufferSource,
    }))

  const device = await api.createDevice({
    platform: 'web',
    webPushSub: JSON.stringify(subscription),
  })
  return device.id
}
