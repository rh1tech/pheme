// Minimal JWT helpers. Decoding is for display/identity only — the server is the
// authority on token validity.

export function decodeUserId(accessToken: string): string | null {
  try {
    const payload = accessToken.split('.')[1]
    const json = atob(payload.replace(/-/g, '+').replace(/_/g, '/'))
    const claims = JSON.parse(json) as { sub?: string }
    return claims.sub ?? null
  } catch {
    return null
  }
}
