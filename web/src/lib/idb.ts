// A tiny IndexedDB key→bytes store for the MLS client state.
//
// localStorage cannot be read from a service worker and caps at ~5MB; the MLS key
// store is binary and can grow, so it lives in IndexedDB. This is a deliberately
// minimal wrapper — one object store, get/set/delete of Uint8Array by string key.

const DB_NAME = 'pheme'
const STORE = 'mls'

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1)
    req.onupgradeneeded = () => {
      req.result.createObjectStore(STORE)
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

function tx<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return open().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const request = run(db.transaction(STORE, mode).objectStore(STORE))
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      }),
  )
}

export function idbGet(key: string): Promise<Uint8Array | undefined> {
  return tx<Uint8Array | undefined>('readonly', (s) => s.get(key))
}

export function idbSet(key: string, value: Uint8Array): Promise<void> {
  return tx('readwrite', (s) => s.put(value, key)).then(() => undefined)
}

export function idbDelete(key: string): Promise<void> {
  return tx('readwrite', (s) => s.delete(key)).then(() => undefined)
}

/**
 * Wipes the whole store: the MLS key state and every cached decrypted message.
 * Used on logout — leaving private keys and plaintext behind on a shared device
 * would defeat the encryption entirely.
 */
export function idbClear(): Promise<void> {
  return tx('readwrite', (s) => s.clear()).then(() => undefined)
}
