// Token persistence in localStorage, shared by the API client and auth context.

const ACCESS_KEY = 'pheme.accessToken'
const REFRESH_KEY = 'pheme.refreshToken'

export interface Tokens {
  accessToken: string
  refreshToken: string
}

export function loadTokens(): Tokens | null {
  const accessToken = localStorage.getItem(ACCESS_KEY)
  const refreshToken = localStorage.getItem(REFRESH_KEY)
  if (!accessToken || !refreshToken) return null
  return { accessToken, refreshToken }
}

export function saveTokens(tokens: Tokens): void {
  localStorage.setItem(ACCESS_KEY, tokens.accessToken)
  localStorage.setItem(REFRESH_KEY, tokens.refreshToken)
}

export function clearTokens(): void {
  localStorage.removeItem(ACCESS_KEY)
  localStorage.removeItem(REFRESH_KEY)
}
