// Minimal JWT helpers. Decoding is for display/identity only — the server is the
// authority on token validity.

interface DecodedClaims {
  sub: string | null
  role: string | null
}

function decode(accessToken: string): DecodedClaims {
  try {
    const payload = accessToken.split('.')[1]
    const json = atob(payload.replace(/-/g, '+').replace(/_/g, '/'))
    const claims = JSON.parse(json) as { sub?: string; role?: string }
    return { sub: claims.sub ?? null, role: claims.role ?? null }
  } catch {
    return { sub: null, role: null }
  }
}

export function decodeUserId(accessToken: string): string | null {
  return decode(accessToken).sub
}

export function decodeRole(accessToken: string): string | null {
  return decode(accessToken).role
}
