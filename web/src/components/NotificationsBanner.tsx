import { useEffect, useState } from 'react'
import { Alert, Button, Group, Text } from '@mantine/core'
import { IconBellOff } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { getWebPushState, registerWebPushDevice } from '../lib/webpush'
import { saveWebDeviceId } from '../lib/device'
import { notifyError, notifySuccess } from '../lib/notify'

/**
 * A persistent warning shown on every authenticated page (rendered in Layout)
 * whenever browser notifications are supported but not currently on. It offers
 * to enable them; when the browser has blocked them it explains that instead.
 * Renders nothing when notifications are already on or unsupported.
 */
export function NotificationsBanner() {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(false)
  const [blocked, setBlocked] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let active = true
    getWebPushState()
      .then((s) => {
        if (!active) return
        const on = s.permission === 'granted' && s.subscribed
        setVisible(s.supported && !on)
        setBlocked(s.permission === 'denied')
      })
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [])

  async function enable() {
    setBusy(true)
    try {
      const deviceId = await registerWebPushDevice()
      saveWebDeviceId(deviceId)
      setVisible(false)
      notifySuccess(t('dashboard.notificationsEnabled'))
    } catch (e) {
      notifyError(t('dashboard.enableFailed'), e)
    } finally {
      setBusy(false)
    }
  }

  if (!visible) return null

  return (
    <Alert
      color={blocked ? 'gray' : 'yellow'}
      variant="light"
      icon={<IconBellOff size={18} />}
      mb="lg"
      p="sm"
    >
      <Group justify="space-between" wrap="wrap" gap="sm">
        <Text size="sm">
          {blocked ? t('channel.notificationsBlocked') : t('common.notificationsOff')}
        </Text>
        {!blocked && (
          <Button size="xs" color="yellow" onClick={enable} loading={busy}>
            {t('dashboard.enableNotifications')}
          </Button>
        )}
      </Group>
    </Alert>
  )
}
