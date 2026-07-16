// Is this tab running the code that is currently deployed?
//
// A tab can live for days. Ours is a chat app people leave open, and a deploy does not reach any of
// them: they keep running the bundle they loaded, against an API that has moved on. So the build
// stamps its id into the bundle (vite.config.ts) and writes the same id to /version.json; the tab
// compares the two and offers to reload when they differ.
//
// It only ever ASKS. Reloading a chat out from under someone — mid-message, mid-call — to save them
// a stale tab is not a trade anyone would take.

/** The id of the bundle THIS tab is running. */
export const BUILD_ID: string =
  // typeof, not a bare read: under vitest there is no define, and an undeclared global throws.
  typeof __BUILD_ID__ === 'string' ? __BUILD_ID__ : 'dev'

/** The release THIS tab is running, for a person to read. 'dev' when not built from a CI tag. */
export const APP_VERSION: string = typeof __APP_VERSION__ === 'string' ? __APP_VERSION__ : 'dev'

/**
 * The id the server is serving right now, or null when it cannot be established — offline, a
 * proxy's error page, a dev server with no version.json. Null is never "out of date": being unable
 * to ask is not evidence of anything, and a prompt on every flaky poll would be a nag.
 */
export async function fetchDeployedBuildId(
  fetchImpl: typeof fetch = fetch,
): Promise<string | null> {
  try {
    // no-store: the whole point is to see past every cache. nginx sends the same header, but a
    // proxy in between was not asked.
    const res = await fetchImpl('/version.json', { cache: 'no-store' })
    if (!res.ok) return null
    const body: unknown = await res.json()
    const buildId = (body as { buildId?: unknown } | null)?.buildId
    return typeof buildId === 'string' && buildId !== '' ? buildId : null
  } catch {
    // Offline, or something that is not JSON. Ask again on the next tick.
    return null
  }
}

/** Whether a tab running `running` is behind what is deployed. */
export function isOutdated(deployed: string | null, running: string): boolean {
  return deployed !== null && deployed !== running
}
