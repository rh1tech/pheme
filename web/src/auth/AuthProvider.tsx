import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, setOnAuthFailure } from '../lib/api'
import { clearTokens, loadTokens, saveTokens } from '../lib/tokens'
import { decodeUserId } from '../lib/jwt'
import { AuthContext, type AuthState } from './context'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [userId, setUserId] = useState<string | null>(() => {
    const tokens = loadTokens()
    return tokens ? decodeUserId(tokens.accessToken) : null
  })

  const logout = useCallback(() => {
    clearTokens()
    setUserId(null)
  }, [])

  // When the API client detects an unrecoverable auth failure, drop session state.
  useEffect(() => {
    setOnAuthFailure(() => setUserId(null))
  }, [])

  const login = useCallback(async (email: string, password: string) => {
    const res = await api.login(email, password)
    saveTokens({ accessToken: res.accessToken, refreshToken: res.refreshToken })
    setUserId(res.userId)
  }, [])

  const register = useCallback(async (email: string, password: string) => {
    const res = await api.register(email, password)
    saveTokens({ accessToken: res.accessToken, refreshToken: res.refreshToken })
    setUserId(res.userId)
  }, [])

  const value = useMemo<AuthState>(
    () => ({ userId, isAuthenticated: userId !== null, login, register, logout }),
    [userId, login, register, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
