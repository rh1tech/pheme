// Which Pheme server this browser is talking to.
//
// The address used to be decided entirely at deploy time — a runtime config file baked into the
// container, or a Vite env var, or localhost. That is right for the one deployment the web app
// ships with and wrong for everybody else: Pheme is federated, anybody can run a server, and a
// person signing in to somebody else's had no way to say so from a page that had already decided.
//
// So it is a field on the sign-in form now, exactly as it is on mobile. The deploy-time value is
// still what the field STARTS as, because a browser that loaded this page from a server has a very
// good guess about which server it means — but it is a guess the person can see and correct.

const KEY = 'pheme.baseUrl'

/** A scheme, per RFC 3986: a LETTER, then letters, digits, `+`, `-`, `.`, then `://`. */
const SCHEME = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//

/** The schemes we can actually speak. */
const SPEAKABLE = ['http://', 'https://']

/**
 * What the deployment thinks the server is: the runtime config a production container writes, then
 * the build-time env for local development. May be empty, and that is a legitimate answer — a build
 * with no compiled default just means the field starts blank and has to be filled in.
 */
export function deployedBaseUrl(): string {
  const runtime = typeof window !== 'undefined' ? window.__PHEME_CONFIG?.apiBase : undefined
  return normalizeServerUrl(runtime || import.meta.env.VITE_API_BASE || '') ?? ''
}

/**
 * Turns what somebody typed into an address, or null if it cannot be one.
 *
 * Forgiving on purpose. This is a string an operator reads out over the phone and a person types
 * before they have an account, so refusing `pheme.example.com` for want of a scheme locks them out
 * of a server that is working perfectly well.
 */
export function normalizeServerUrl(input: string): string | null {
  const trimmed = input.trim()
  if (trimmed === '') return null

  // `10.0.2.2:8080` looks like scheme-and-path to a naive parser, which is why this tests for a
  // scheme rather than for a colon: a scheme starts with a letter, a port does not.
  const withScheme = SCHEME.test(trimmed) ? trimmed : `https://${trimmed}`
  if (!SPEAKABLE.some((s) => withScheme.toLowerCase().startsWith(s))) return null

  // Checked on the TEXT, before the URL parser sees it. For a special scheme the WHATWG parser
  // forgives extra slashes — it reads `https:///a/path` as host `a` and path `/path`, so a typed
  // slash silently promotes the first path segment to a hostname and the address points somewhere
  // nobody asked for. Mobile rejects that outright, and the two have to agree.
  const authority = withScheme.slice(withScheme.indexOf('://') + 3)
  if (authority === '' || authority.startsWith('/')) return null

  let url: URL
  try {
    url = new URL(withScheme)
  } catch {
    return null
  }
  if (url.hostname === '') return null

  // A pasted browser URL brings a trailing slash along, and joining "/v1/..." onto it yields
  // "//v1/...", which some servers route and some do not — a failure with nothing to see.
  const normalized = withScheme.replace(/\/+$/, '')
  return normalized === '' ? null : normalized
}

/** Whether this could be a server address. The form's validator. */
export function isValidServerUrl(input: string): boolean {
  return normalizeServerUrl(input) !== null
}

/**
 * What a previous sign-in chose, if anything. What the sign-in form's field starts as, before
 * falling back to the deployment's guess.
 */
export function storedBaseUrl(): string | null {
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem(KEY)
}

/**
 * The address in force: what a previous sign-in chose, falling back to what the deployment guessed.
 *
 * Read on every request rather than captured once at module load, so choosing a server on the
 * sign-in form takes effect for the sign-in itself.
 */
export function apiBase(): string {
  if (typeof localStorage === 'undefined') return deployedBaseUrl()
  return localStorage.getItem(KEY) || deployedBaseUrl()
}

/** Remembers the server. Stores the normalized form, never the raw text. */
export function saveBaseUrl(input: string): string | null {
  const normalized = normalizeServerUrl(input)
  if (normalized === null) return null
  localStorage.setItem(KEY, normalized)
  return normalized
}
