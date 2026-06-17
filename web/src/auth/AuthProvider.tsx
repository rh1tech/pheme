import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, setOnAuthFailure } from '../lib/api'
import { clearTokens, loadTokens, saveTokens } from '../lib/tokens'
import { decodeRole, decodeUserId } from '../lib/jwt'
import { AuthContext, type AuthState } from './context'

interface Identity {
  userId: string | null
  role: string | null
}

function identityFromTokens(): Identity {
  const tokens = loadTokens()
  if (!tokens) return { userId: null, role: null }
  return { userId: decodeUserId(tokens.accessToken), role: decodeRole(tokens.accessToken) }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [identity, setIdentity] = useState<Identity>(() => identityFromTokens())

  const logout = useCallback(() => {
    clearTokens()
    setIdentity({ userId: null, role: null })
  }, [])

  // When the API client detects an unrecoverable auth failure, drop session state.
  useEffect(() => {
    setOnAuthFailure(() => setIdentity({ userId: null, role: null }))
  }, [])

  const login = useCallback(async (email: string, password: string) => {
    const res = await api.login(email, password)
    saveTokens({ accessToken: res.accessToken, refreshToken: res.refreshToken })
    setIdentity({ userId: res.userId, role: res.role })
  }, [])

  const register = useCallback(async (email: string, password: string) => {
    const res = await api.register(email, password)
    saveTokens({ accessToken: res.accessToken, refreshToken: res.refreshToken })
    setIdentity({ userId: res.userId, role: res.role })
  }, [])

  const value = useMemo<AuthState>(
    () => ({
      userId: identity.userId,
      role: identity.role,
      isAuthenticated: identity.userId !== null,
      isAdmin: identity.role === 'admin',
      login,
      register,
      logout,
    }),
    [identity, login, register, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
