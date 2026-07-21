// A `username@host` handle names someone on another Pheme server. The host may be
// a full domain (`hub.example.com`) or a short nodelist alias (`pheme1`) —
// the server resolves either to a domain and then the username to an id. Local
// user search never returns cross-host people (it queries one host's directory),
// so a federated member is added by typing their whole handle.
//
// Kept deliberately permissive: a username is 3–30 chars, and the host is anything
// domain- or alias-shaped. A handle that resolves to nobody just comes back as
// "user not found" — the regex only decides whether to OFFER the remote add, not
// whether the person exists.
const REMOTE_HANDLE = /^[a-zA-Z0-9_.]{3,30}@[a-zA-Z0-9][a-zA-Z0-9.-]{1,}$/

/** Returns the trimmed handle if the input looks like `username@host`, else null. */
export function remoteHandle(input: string): string | null {
  const trimmed = input.trim()
  return REMOTE_HANDLE.test(trimmed) ? trimmed : null
}
