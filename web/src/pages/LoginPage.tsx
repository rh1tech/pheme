import { useState } from 'react'
import {
  Anchor,
  Button,
  Card,
  Center,
  Group,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../auth/context'
import { ApiError } from '../lib/api'
import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { ThemeToggle } from '../components/ThemeToggle'

export function LoginPage() {
  const { login, register } = useAuth()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)

  async function submit() {
    setLoading(true)
    try {
      if (mode === 'login') {
        await login(email, password)
      } else {
        await register(email, password)
      }
      navigate('/', { replace: true })
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : t('auth.requestFailed')
      notifications.show({ color: 'red', message: msg })
    } finally {
      setLoading(false)
    }
  }

  return (
    <Center mih="100vh">
      <Card withBorder padding="xl" w={380}>
        <Stack>
          <Group justify="space-between" align="flex-start">
            <div>
              <Title order={3}>{t('common.appName')}</Title>
              <Text size="sm" c="dimmed">
                {mode === 'login' ? t('auth.signInSubtitle') : t('auth.registerSubtitle')}
              </Text>
            </div>
            <Group gap="xs">
              <ThemeToggle />
              <LanguageSwitcher />
            </Group>
          </Group>
          <TextInput
            label={t('auth.email')}
            type="email"
            value={email}
            onChange={(e) => setEmail(e.currentTarget.value)}
          />
          <PasswordInput
            label={t('auth.password')}
            value={password}
            onChange={(e) => setPassword(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && submit()}
          />
          <Button onClick={submit} loading={loading} fullWidth>
            {mode === 'login' ? t('auth.signIn') : t('auth.register')}
          </Button>
          <Text size="sm" c="dimmed" ta="center">
            {mode === 'login' ? t('auth.noAccount') : t('auth.haveAccount')}
            <Anchor onClick={() => setMode(mode === 'login' ? 'register' : 'login')}>
              {mode === 'login' ? t('auth.register') : t('auth.signIn')}
            </Anchor>
          </Text>
        </Stack>
      </Card>
    </Center>
  )
}
