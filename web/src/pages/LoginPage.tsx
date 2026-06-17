import { useState } from 'react'
import {
  Anchor,
  Button,
  Card,
  Center,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/context'
import { ApiError } from '../lib/api'

export function LoginPage() {
  const { login, register } = useAuth()
  const navigate = useNavigate()
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
      const msg = err instanceof ApiError ? err.message : 'request failed'
      notifications.show({ color: 'red', message: msg })
    } finally {
      setLoading(false)
    }
  }

  return (
    <Center mih="80vh">
      <Card withBorder padding="xl" w={380}>
        <Stack>
          <div>
            <Title order={3}>Pheme</Title>
            <Text size="sm" c="dimmed">
              {mode === 'login' ? 'Sign in to your account' : 'Create an account'}
            </Text>
          </div>
          <TextInput
            label="Email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.currentTarget.value)}
          />
          <PasswordInput
            label="Password"
            value={password}
            onChange={(e) => setPassword(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && submit()}
          />
          <Button onClick={submit} loading={loading} fullWidth>
            {mode === 'login' ? 'Sign in' : 'Register'}
          </Button>
          <Text size="sm" c="dimmed" ta="center">
            {mode === 'login' ? "Don't have an account? " : 'Already registered? '}
            <Anchor onClick={() => setMode(mode === 'login' ? 'register' : 'login')}>
              {mode === 'login' ? 'Register' : 'Sign in'}
            </Anchor>
          </Text>
        </Stack>
      </Card>
    </Center>
  )
}
