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

/**
 * Writes several entries in ONE transaction, so they can never be observed apart.
 *
 * The MLS state and its version must move together: if a tab dies between two
 * separate writes, the state advances while the version does not, and another tab
 * then concludes nothing changed and mutates on top of state it thinks is current —
 * silently reusing key material that has already been consumed.
 */
export function idbSetMany(entries: Array<[string, Uint8Array]>): Promise<void> {
  return open().then(
    (db) =>
      new Promise<void>((resolve, reject) => {
        const transaction = db.transaction(STORE, 'readwrite')
        const store = transaction.objectStore(STORE)
        for (const [key, value] of entries) store.put(value, key)
        transaction.oncomplete = () => resolve()
        transaction.onerror = () => reject(transaction.error)
        transaction.onabort = () => reject(transaction.error)
      }),
  )
}

export function idbDelete(key: string): Promise<void> {
  return tx('readwrite', (s) => s.delete(key)).then(() => undefined)
}

/**
 * Wipes the whole store — the MLS key state and every cached decrypted message —
 * and writes `keep` back, all in ONE transaction.
 *
 * The two halves cannot be separated. The wipe is what destroys the keys; the entry
 * written back is the epoch that tells any session still running in another tab that
 * its keys are gone. Done as two transactions, an interruption between them leaves no
 * epoch at all — it reads back as zero, which is exactly what a stale session is
 * carrying, so that session would conclude it was still live and write the destroyed
 * keys back to disk.
 */
export function idbClearExcept(keep: Array<[string, Uint8Array]>): Promise<void> {
  return open().then(
    (db) =>
      new Promise<void>((resolve, reject) => {
        const transaction = db.transaction(STORE, 'readwrite')
        const store = transaction.objectStore(STORE)
        store.clear()
        for (const [key, value] of keep) store.put(value, key)
        transaction.oncomplete = () => resolve()
        transaction.onerror = () => reject(transaction.error)
        transaction.onabort = () => reject(transaction.error)
      }),
  )
}
