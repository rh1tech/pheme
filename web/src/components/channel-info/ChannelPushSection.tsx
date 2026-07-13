import { useEffect, useState } from 'react'
import { Alert, Badge, Button, Card, Group, Stack, Text } from '@mantine/core'
import { IconBellCheck, IconDeviceMobile } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import { loadWebDeviceId, saveWebDeviceId } from '../../lib/device'
import { registerWebPushDevice, webPushAvailability } from '../../lib/webpush'

interface ChannelPushSectionProps {
  channelId: string
}

type SubStatus = 'active' | 'pending' | 'none'

/** Whether this browser receives push notifications for the channel. */
export function ChannelPushSection({ channelId }: ChannelPushSectionProps) {
  const { t } = useTranslation()
  const availability = webPushAvailability()
  const [status, setStatus] = useState<SubStatus>('none')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let active = true
    const deviceId = loadWebDeviceId()
    if (!deviceId) return // no device registered → status stays "none"
    api
      .channelSubscription(channelId, deviceId)
      .then((s) => active && setStatus(s))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [channelId])

  async function subscribe() {
    setBusy(true)
    try {
      // Always (re)register: the server upserts the web device by its push
      // endpoint, so this self-heals a stale cached id or a deleted device.
      const deviceId = await registerWebPushDevice()
      saveWebDeviceId(deviceId)
      await api.subscribe(channelId, deviceId)
      setStatus(await api.channelSubscription(channelId, deviceId))
      notifySuccess(t('channel.browserSubscribed'))
    } catch (e) {
      notifyError(t('channel.subscribeFailed'), e)
    } finally {
      setBusy(false)
    }
  }

  async function unsubscribe() {
    const deviceId = loadWebDeviceId()
    if (!deviceId) {
      setStatus('none')
      return
    }
    setBusy(true)
    try {
      await api.unsubscribe(channelId, deviceId)
      setStatus('none')
      notifySuccess(t('channel.unsubscribed'))
    } catch (e) {
      notifyError(t('channel.unsubscribeFailed'), e)
    } finally {
      setBusy(false)
    }
  }

  const supported = availability === 'supported'

  return (
    <Card withBorder padding="md">
      <Group justify="space-between" wrap="nowrap">
        <Stack gap={2}>
          <Group gap="xs">
            <Text fw={600}>{t('channel.subscribeTitle')}</Text>
            {supported && status === 'active' && (
              <Badge color="teal" variant="light" leftSection={<IconBellCheck size={14} />}>
                {t('channel.subscribed')}
              </Badge>
            )}
            {supported && status === 'pending' && (
              <Badge color="yellow" variant="light">
                {t('channel.subscriptionPending')}
              </Badge>
            )}
          </Group>
          <Text size="sm" c="dimmed">
            {t('channel.subscribeDescription')}
          </Text>
        </Stack>

        {supported &&
          (status === 'none' ? (
            <Button variant="outline" loading={busy} onClick={subscribe}>
              {t('channel.subscribeBrowser')}
            </Button>
          ) : (
            <Button variant="subtle" color="red" loading={busy} onClick={unsubscribe}>
              {t('channel.unsubscribe')}
            </Button>
          ))}
      </Group>

      {!supported && (
        <Alert
          mt="sm"
          variant="light"
          color={availability === 'ios-needs-install' ? 'blue' : 'gray'}
          icon={<IconDeviceMobile size={18} />}
        >
          {availability === 'ios-needs-install'
            ? t('channel.subscribeIosHint')
            : t('channel.subscribeUnsupported')}
        </Alert>
      )}
    </Card>
  )
}
