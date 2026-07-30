import { useEffect, useState } from 'react'
import {
  Anchor,
  Button,
  Card,
  Center,
  Group,
  PasswordInput,
  PinInput,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { useAuth } from '../auth/context'
import { ApiError, api } from '../lib/api'
import { notifyError } from '../lib/notify'
import { checkPassword } from '../lib/password'
import { useCountdown } from '../hooks/useCountdown'
import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { ThemeToggle } from '../components/ThemeToggle'
import { Logo } from '../components/Logo'
import { PasswordStrength } from '../components/PasswordStrength'
import { ServerInput } from '../components/ServerInput'
import { deployedBaseUrl, isValidServerUrl, saveBaseUrl, storedBaseUrl } from '../lib/server'

const RESEND_SECONDS = 120

// The server's verdict on ONE code. The code it applies to is stored with it, so a verdict
// is never shown against a code the visitor has since edited — the stale-result bug that a
// bare valid/invalid flag invites.
type InviteVerdict = { code: string; valid: boolean; reason?: string }

export function LoginPage() {
  const { login, register, verifyEmail, isAuthenticated } = useAuth()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [searchParams] = useSearchParams()
  // An invitation link lands here with the code in the query. Arriving with one means the
  // visitor came to sign UP, so the form opens on register rather than making them find the
  // toggle — and the code is theirs to correct if the link was mangled in transit.
  const inviteFromLink = searchParams.get('invite')?.trim() ?? ''
  const [mode, setMode] = useState<'login' | 'register'>(inviteFromLink ? 'register' : 'login')
  const [invite, setInvite] = useState(inviteFromLink)
  const [inviteOnly, setInviteOnly] = useState(false)
  const [inviteVerdict, setInviteVerdict] = useState<InviteVerdict | null>(null)
  const [step, setStep] = useState<'credentials' | 'verify'>('credentials')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [loading, setLoading] = useState(false)
  const [cooldown, startCooldown] = useCountdown()
  // Seeded from whatever this browser already points at — the address a previous sign-in chose, or
  // the one this deployment was configured with. Seeded, not assumed: it is a field the person can
  // see and correct, and on a build with no configured default it starts empty.
  const [server, setServer] = useState(() => storedBaseUrl() || deployedBaseUrl())
  // Set the moment a sign-in succeeds; see the redirect below.
  const [signedIn, setSignedIn] = useState(false)

  // Whether this server takes open registrations. Asked once, and asked of whichever server
  // the address field currently points at — the answer belongs to that server, not to this
  // build, so switching servers has to re-ask.
  useEffect(() => {
    if (!isValidServerUrl(server)) return
    let live = true
    saveBaseUrl(server)
    api
      .registrationInfo()
      .then((info) => {
        if (live) setInviteOnly(info.inviteOnly)
      })
      // A server too old to answer, or one that is briefly unreachable, is treated as open:
      // the invite field simply does not appear, and a register attempt still gets the real
      // verdict from /register. Guessing "closed" here would block signup on a hiccup.
      .catch(() => {
        if (live) setInviteOnly(false)
      })
    return () => {
      live = false
    }
  }, [server])

  // Check the code once it settles, so a spent link says so before the form is filled in.
  useEffect(() => {
    const code = invite.trim()
    if (!inviteOnly || mode !== 'register' || code === '') return
    let live = true
    const timer = setTimeout(() => {
      api
        .checkInvite(code)
        .then((res) => {
          if (live) setInviteVerdict({ code, valid: res.valid, reason: res.reason })
        })
        // Silence, not a verdict: /register is the one that decides, and calling a good
        // invitation bad because the check request failed would be worse than saying nothing.
        .catch(() => undefined)
    }, 400)
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [invite, inviteOnly, mode])

  // Leaving after a successful sign-in is decided by STATE, and by nothing else.
  //
  // It used to be an imperative navigate(), and an imperative navigation can be lost: something
  // else navigating at the same moment cancels it ("Navigation to / is interrupted by another
  // navigation to /"). When that happened the person was fully signed in — the server had returned
  // 200 and the tokens were stored — and still looking at the login form, with no error to explain
  // it and nothing to do but try again.
  //
  // This redirect must be the ONLY one. Keeping the navigate() alongside it, which is what the
  // first version of this fix did, means a sign-in fires two navigations to the same place, and the
  // straggler cancels whatever the person did next — the click they made the instant the app
  // appeared, silently undone.
  //
  // Deliberately gated on having just signed in HERE, rather than on being authenticated at all.
  // Redirecting every authenticated visitor away from /login would make it impossible to reach
  // this form to sign in as somebody else — which is exactly what an admin does to check that a
  // blocked account cannot get back in, and what anyone does to switch accounts.
  if (signedIn && isAuthenticated) return <Navigate to="/" replace />

  function fail(err: unknown) {
    notifyError(err instanceof ApiError ? err.message : t('auth.requestFailed'))
  }

  async function submitCredentials() {
    if (!canSubmitCredentials) return
    // The server first, and before any request: everything below sends credentials, and a request
    // needs to know where it is going. Signing in against the previous address and only then
    // switching would hand one server the password meant for another.
    saveBaseUrl(server)
    setLoading(true)
    try {
      if (mode === 'login') {
        await login(email, password)
        setSignedIn(true)
      } else {
        await register(email, password, invite.trim() || undefined)
        setCode('')
        setStep('verify')
        startCooldown(RESEND_SECONDS)
      }
    } catch (err) {
      fail(err)
    } finally {
      setLoading(false)
    }
  }

  async function submitCode(value: string) {
    setLoading(true)
    try {
      await verifyEmail(email, value)
      setSignedIn(true)
    } catch (err) {
      fail(err)
    } finally {
      setLoading(false)
    }
  }

  async function resend() {
    if (cooldown > 0 || loading) return
    setLoading(true)
    try {
      await register(email, password, invite.trim() || undefined)
      startCooldown(RESEND_SECONDS)
    } catch (err) {
      fail(err)
    } finally {
      setLoading(false)
    }
  }

  function switchMode() {
    setMode(mode === 'login' ? 'register' : 'login')
    setStep('credentials')
    setPassword('')
    setCode('')
  }

  const needsInvite = mode === 'register' && inviteOnly
  // Derived, not stored: a verdict counts only while it still describes what is in the box.
  const verdict = inviteVerdict?.code === invite.trim() ? inviteVerdict : null
  const checkingInvite = needsInvite && invite.trim() !== '' && verdict === null
  const canSubmitCredentials =
    email.trim() !== '' &&
    isValidServerUrl(server) &&
    (mode === 'login' || checkPassword(password).acceptable) &&
    // A code we already know to be spent is not worth a round trip; an unchecked one is, so
    // only a verdict of "bad" blocks the button.
    (!needsInvite || (invite.trim() !== '' && verdict?.valid !== false))

  const inviteReason = verdict && !verdict.valid ? (verdict.reason ?? 'unknown') : null
  const inviteError = inviteReason
    ? t(`auth.invite${inviteReason.charAt(0).toUpperCase()}${inviteReason.slice(1)}`)
    : null

  return (
    <Center mih="100vh" p="md">
      <Stack w={400} gap="lg">
        <Group justify="space-between">
          <Logo size="lg" />
          <Group gap="xs">
            <ThemeToggle />
            <LanguageSwitcher />
          </Group>
        </Group>
        <Card withBorder padding="xl" shadow="md">
          {step === 'verify' ? (
            <Stack>
              <Text fw={600} fz="lg">
                {t('auth.verifyTitle')}
              </Text>
              <Text size="sm" c="dimmed">
                <Trans i18nKey="auth.verifySubtitle" values={{ email }} components={{ bold: <b /> }} />
              </Text>
              <Group justify="center" my="sm">
                <PinInput
                  length={6}
                  type="number"
                  oneTimeCode
                  value={code}
                  onChange={setCode}
                  onComplete={submitCode}
                  disabled={loading}
                />
              </Group>
              <Button
                onClick={() => submitCode(code)}
                loading={loading}
                disabled={code.length !== 6}
                fullWidth
              >
                {t('auth.verifyAction')}
              </Button>
              <Group justify="space-between">
                <Anchor size="sm" onClick={() => setStep('credentials')}>
                  {t('auth.back')}
                </Anchor>
                <Anchor size="sm" c={cooldown > 0 ? 'dimmed' : undefined} onClick={resend}>
                  {cooldown > 0 ? t('auth.resendIn', { seconds: cooldown }) : t('auth.resend')}
                </Anchor>
              </Group>
            </Stack>
          ) : (
            <Stack>
              <Text fw={600} fz="lg">
                {mode === 'login' ? t('auth.signInSubtitle') : t('auth.registerSubtitle')}
              </Text>
              <TextInput
                label={t('auth.email')}
                type="email"
                autoFocus
                value={email}
                onChange={(e) => setEmail(e.currentTarget.value)}
              />
              <div>
                <PasswordInput
                  label={t('auth.password')}
                  value={password}
                  onChange={(e) => setPassword(e.currentTarget.value)}
                  onKeyDown={(e) => e.key === 'Enter' && submitCredentials()}
                />
                {mode === 'register' && <PasswordStrength value={password} />}
              </div>
              {needsInvite && (
                <TextInput
                  label={t('auth.invite')}
                  placeholder={t('auth.invitePlaceholder')}
                  value={invite}
                  onChange={(e) => setInvite(e.currentTarget.value)}
                  onKeyDown={(e) => e.key === 'Enter' && submitCredentials()}
                  error={inviteError}
                  description={
                    invite.trim() === ''
                      ? t('auth.inviteRequired')
                      : checkingInvite
                        ? t('auth.inviteChecking')
                        : verdict?.valid
                          ? t('auth.inviteValid')
                          : undefined
                  }
                />
              )}
              <ServerInput
                value={server}
                onChange={setServer}
                disabled={loading}
                onEnter={submitCredentials}
              />
              {mode === 'login' && (
                <Anchor size="sm" onClick={() => navigate('/forgot-password')}>
                  {t('auth.forgotPassword')}
                </Anchor>
              )}
              <Button
                onClick={submitCredentials}
                loading={loading}
                disabled={!canSubmitCredentials}
                fullWidth
                mt="xs"
              >
                {mode === 'login' ? t('auth.signIn') : t('auth.register')}
              </Button>
              <Text size="sm" c="dimmed" ta="center">
                {mode === 'login' ? t('auth.noAccount') : t('auth.haveAccount')}
                <Anchor fw={500} onClick={switchMode}>
                  {mode === 'login' ? t('auth.register') : t('auth.signIn')}
                </Anchor>
              </Text>
            </Stack>
          )}
        </Card>
      </Stack>
    </Center>
  )
}
