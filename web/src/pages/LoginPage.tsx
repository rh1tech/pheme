import { useState } from 'react'
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
import { Navigate, useNavigate } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { useAuth } from '../auth/context'
import { ApiError } from '../lib/api'
import { notifyError } from '../lib/notify'
import { checkPassword } from '../lib/password'
import { useCountdown } from '../hooks/useCountdown'
import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { ThemeToggle } from '../components/ThemeToggle'
import { Logo } from '../components/Logo'
import { PasswordStrength } from '../components/PasswordStrength'

const RESEND_SECONDS = 120

export function LoginPage() {
  const { login, register, verifyEmail, isAuthenticated } = useAuth()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [step, setStep] = useState<'credentials' | 'verify'>('credentials')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [loading, setLoading] = useState(false)
  const [cooldown, startCooldown] = useCountdown()
  // Set the moment a sign-in succeeds; see the redirect below.
  const [signedIn, setSignedIn] = useState(false)

  // Leaving after a successful sign-in is decided by STATE, not by a call that has to land.
  //
  // It used to depend solely on the imperative navigate() below, and an imperative navigation can
  // be lost: something else navigating at the same moment cancels it ("Navigation to / is
  // interrupted by another navigation to /"). When that happened the person was fully signed in —
  // the server had returned 200 and the tokens were stored — and still looking at the login form,
  // with no error to explain it and nothing to do but try again.
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
    setLoading(true)
    try {
      if (mode === 'login') {
        await login(email, password)
        setSignedIn(true)
        navigate('/', { replace: true })
      } else {
        await register(email, password)
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
      navigate('/', { replace: true })
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
      await register(email, password)
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

  const canSubmitCredentials =
    email.trim() !== '' && (mode === 'login' || checkPassword(password).acceptable)

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
