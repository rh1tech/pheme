// Client-side mirror of the server password policy (internal/auth/password_policy.go).
// The server remains the source of truth for acceptance; this drives the strength
// meter and lets the UI disable submit before a round-trip.

/** Minimum password length, matching the server's MinPasswordLength. */
export const MIN_PASSWORD_LENGTH = 8

// A small blocklist mirroring the server's common-password set.
const COMMON_PASSWORDS = new Set([
  'password',
  'password1',
  'password123',
  'passw0rd',
  '12345678',
  '123456789',
  '1234567890',
  'qwerty123',
  'qwertyuiop',
  '1q2w3e4r',
  '1qaz2wsx',
  'iloveyou',
  'admin123',
  'welcome1',
  'welcome123',
  'letmein1',
  'abc12345',
  'football',
  'baseball',
  'sunshine',
  'princess',
  'trustno1',
  'starwars',
  'whatever',
  'changeme',
  'superman',
  '11111111',
  '00000000',
])

function characterClasses(pw: string): number {
  let n = 0
  if (/[a-z]/.test(pw)) n++
  if (/[A-Z]/.test(pw)) n++
  if (/[0-9]/.test(pw)) n++
  if (/[^a-zA-Z0-9]/.test(pw)) n++
  return n
}

export interface PasswordCheck {
  /** Strength score from 0 (empty) to 4 (strong), for the meter. */
  score: number
  /** Whether the password satisfies the minimum policy. */
  acceptable: boolean
}

/** Evaluates a password against the policy and produces a 0–4 strength score. */
export function checkPassword(pw: string): PasswordCheck {
  if (!pw) return { score: 0, acceptable: false }
  const classes = characterClasses(pw)
  const common = COMMON_PASSWORDS.has(pw.toLowerCase())
  const acceptable = pw.length >= MIN_PASSWORD_LENGTH && classes >= 2 && !common

  let score = 0
  if (pw.length >= MIN_PASSWORD_LENGTH) score++
  if (pw.length >= 12) score++
  if (classes >= 2) score++
  if (classes >= 3) score++
  if (common) score = Math.min(score, 1)
  return { score: Math.min(score, 4), acceptable }
}
