import { createContext, useContext } from 'react'

export interface AuthState {
  userId: string | null
  role: string | null
  isAuthenticated: boolean
  isAdmin: boolean
  login: (email: string, password: string) => Promise<void>
  /** Starts registration: emails a verification code. Does not log in. */
  register: (email: string, password: string) => Promise<void>
  /** Confirms the emailed code, creating the account and logging in. */
  verifyEmail: (email: string, code: string) => Promise<void>
  /** Confirms a reset code, sets the new password, and logs in. */
  resetPassword: (email: string, code: string, newPassword: string) => Promise<void>
  logout: () => void
}

export const AuthContext = createContext<AuthState | null>(null)

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
