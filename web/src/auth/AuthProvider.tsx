import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, setOnAuthFailure } from '../lib/api'
import { clearTokens, loadTokens, saveTokens } from '../lib/tokens'
import { wipeLocalKeys } from '../lib/mls'
import { notifyError } from '../lib/notify'
import i18n from '../i18n'
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

  // Signing out must take the encryption keys and the decrypted message cache with
  // it. They are precisely what E2EE protects, and leaving them in IndexedDB on a
  // shared device would let the next person read this account's chats — and let a
  // stale in-memory MLS session encrypt their messages under this identity. The
  // reload guarantees no module-level state survives into the next session.
  //
  // A wipe can genuinely fail (blocked IndexedDB, quota, private browsing). Saying
  // nothing would be the worst outcome: the user walks away from a shared machine
  // believing their keys are gone while they are still on disk. So a failure is
  // surfaced — the toast lives above the routes, so it survives the redirect the auth
  // guard performs the moment the identity is cleared, and the user reads it on the
  // login page. Clearing the identity is what navigates; the wipe cannot prevent that.
  const logout = useCallback(() => {
    clearTokens()
    setIdentity({ userId: null, role: null })
    wipeLocalKeys()
      .then(() => window.location.replace('/login'))
      .catch((err: unknown) => notifyError(i18n.t('common.logoutWipeFailed'), err))
  }, [])

  // When the API client detects an unrecoverable auth failure, drop session state.
  useEffect(() => {
    setOnAuthFailure(() => setIdentity({ userId: null, role: null }))
  }, [])

  const applyTokens = useCallback((res: { accessToken: string; refreshToken: string; userId: string; role: string }) => {
    saveTokens({ accessToken: res.accessToken, refreshToken: res.refreshToken })
    setIdentity({ userId: res.userId, role: res.role })
  }, [])

  const login = useCallback(
    async (email: string, password: string) => {
      applyTokens(await api.login(email, password))
    },
    [applyTokens],
  )

  // Registration only triggers a verification email; the account is created (and
  // the user logged in) once the code is confirmed via verifyEmail.
  const register = useCallback(async (email: string, password: string) => {
    await api.register(email, password)
  }, [])

  const verifyEmail = useCallback(
    async (email: string, code: string) => {
      applyTokens(await api.verifyEmail(email, code))
    },
    [applyTokens],
  )

  const resetPassword = useCallback(
    async (email: string, code: string, newPassword: string) => {
      applyTokens(await api.resetPassword(email, code, newPassword))
    },
    [applyTokens],
  )

  const value = useMemo<AuthState>(
    () => ({
      userId: identity.userId,
      role: identity.role,
      isAuthenticated: identity.userId !== null,
      isAdmin: identity.role === 'admin',
      login,
      register,
      verifyEmail,
      resetPassword,
      logout,
    }),
    [identity, login, register, verifyEmail, resetPassword, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
