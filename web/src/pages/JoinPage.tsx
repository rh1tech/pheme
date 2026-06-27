import { useEffect, useRef } from 'react'
import { Center, Loader, Stack, Text } from '@mantine/core'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { notifyError, notifySuccess } from '../lib/notify'

/**
 * Resolves a `/join?ref=<triggerId|phetag>` deep link (from a shared link or a
 * scanned QR code): joins the channel and redirects to it, or back to the
 * dashboard on failure.
 */
export function JoinPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const ref = params.get('ref')?.trim() ?? ''
  const handled = useRef(false)

  useEffect(() => {
    if (handled.current) return
    handled.current = true
    if (!ref) {
      navigate('/', { replace: true })
      return
    }
    api
      .joinChannel(ref)
      .then((r) => {
        notifySuccess(t('dashboard.joined'))
        navigate(`/channels/${r.channel.id}`, { replace: true })
      })
      .catch((e) => {
        notifyError(t('dashboard.joinFailed'), e)
        navigate('/', { replace: true })
      })
  }, [ref, navigate, t])

  return (
    <Center mih="40vh">
      <Stack align="center" gap="sm">
        <Loader />
        <Text c="dimmed" size="sm">
          {t('dashboard.addChannel')}…
        </Text>
      </Stack>
    </Center>
  )
}
