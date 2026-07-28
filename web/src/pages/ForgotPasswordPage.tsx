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
import { useNavigate } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { useAuth } from '../auth/context'
import { api, ApiError } from '../lib/api'
import { notifyError, notifySuccess } from '../lib/notify'
import { checkPassword } from '../lib/password'
import { useCountdown } from '../hooks/useCountdown'
import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { ThemeToggle } from '../components/ThemeToggle'
import { Logo } from '../components/Logo'
import { PasswordStrength } from '../components/PasswordStrength'
import { ServerInput } from '../components/ServerInput'
import { deployedBaseUrl, isValidServerUrl, saveBaseUrl, storedBaseUrl } from '../lib/server'

const RESEND_SECONDS = 120

export function ForgotPasswordPage() {
  const { resetPassword } = useAuth()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [step, setStep] = useState<'request' | 'reset'>('request')
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [cooldown, startCooldown] = useCountdown()
  // Needed here for the same reason as on the sign-in form, and arguably more: a reset code is sent
  // by the server that holds the account, so asking the wrong one produces silence rather than an
  // error — no code arrives and nothing says why.
  const [server, setServer] = useState(() => storedBaseUrl() || deployedBaseUrl())

  function fail(err: unknown) {
    notifyError(err instanceof ApiError ? err.message : t('auth.requestFailed'))
  }

  async function requestCode() {
    if (!canRequest) return
    saveBaseUrl(server)
    setLoading(true)
    try {
      await api.forgotPassword(email)
      setStep('reset')
      startCooldown(RESEND_SECONDS)
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
      await api.forgotPassword(email)
      startCooldown(RESEND_SECONDS)
    } catch (err) {
      fail(err)
    } finally {
      setLoading(false)
    }
  }

  async function submitReset() {
    if (!canReset) return
    setLoading(true)
    try {
      await resetPassword(email, code, password)
      notifySuccess(t('auth.resetDone'))
      navigate('/', { replace: true })
    } catch (err) {
      fail(err)
    } finally {
      setLoading(false)
    }
  }

  const canRequest = email.trim() !== '' && isValidServerUrl(server)
  const canReset = code.length === 6 && checkPassword(password).acceptable

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
          {step === 'request' ? (
            <Stack>
              <Text fw={600} fz="lg">
                {t('auth.forgotTitle')}
              </Text>
              <Text size="sm" c="dimmed">
                {t('auth.forgotSubtitle')}
              </Text>
              <TextInput
                label={t('auth.email')}
                type="email"
                autoFocus
                value={email}
                onChange={(e) => setEmail(e.currentTarget.value)}
                onKeyDown={(e) => e.key === 'Enter' && requestCode()}
              />
              <ServerInput
                value={server}
                onChange={setServer}
                disabled={loading}
                onEnter={requestCode}
              />
              <Button onClick={requestCode} loading={loading} disabled={!canRequest} fullWidth mt="xs">
                {t('auth.sendCode')}
              </Button>
              <Anchor size="sm" ta="center" onClick={() => navigate('/login')}>
                {t('auth.backToSignIn')}
              </Anchor>
            </Stack>
          ) : (
            <Stack>
              <Text fw={600} fz="lg">
                {t('auth.resetTitle')}
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
                  disabled={loading}
                />
              </Group>
              <div>
                <PasswordInput
                  label={t('auth.newPassword')}
                  value={password}
                  onChange={(e) => setPassword(e.currentTarget.value)}
                />
                <PasswordStrength value={password} />
              </div>
              <Button onClick={submitReset} loading={loading} disabled={!canReset} fullWidth mt="xs">
                {t('auth.resetAction')}
              </Button>
              <Group justify="space-between">
                <Anchor size="sm" onClick={() => navigate('/login')}>
                  {t('auth.backToSignIn')}
                </Anchor>
                <Anchor size="sm" c={cooldown > 0 ? 'dimmed' : undefined} onClick={resend}>
                  {cooldown > 0 ? t('auth.resendIn', { seconds: cooldown }) : t('auth.resend')}
                </Anchor>
              </Group>
            </Stack>
          )}
        </Card>
      </Stack>
    </Center>
  )
}
