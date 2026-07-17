import { useEffect, useState } from 'react'
import { Alert, Badge, Button, Group, Loader, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconDeviceDesktop, IconShieldCheck, IconShieldOff } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../auth/context'
import { api } from '../../lib/api'
import { backupExists, terminateOwnDevice } from '../../lib/mls'
import { loadMlsDeviceId } from '../../lib/device'
import { ResponsiveModal } from '../ResponsiveModal'
import type { MLSDevice } from '../../lib/types'

interface SecurityModalProps {
  opened: boolean
  onClose: () => void
}

/** "{{when}}" for the "Active {{when}}" line — a coarse relative time, localised, no library. */
function relative(iso: string, lang: string): string {
  const then = new Date(iso).getTime()
  if (!Number.isFinite(then)) return ''
  const secs = Math.round((then - Date.now()) / 1000)
  const rtf = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' })
  const abs = Math.abs(secs)
  if (abs < 60) return rtf.format(Math.round(secs), 'second')
  if (abs < 3600) return rtf.format(Math.round(secs / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(secs / 3600), 'hour')
  return rtf.format(Math.round(secs / 86400), 'day')
}

/**
 * "Devices & security": the user's own devices, and their end-to-end-encryption status (backup on,
 * automatic history sync). Read-only for now — a place to SEE the account's security posture. Opened
 * from the sidebar menu.
 */
export function SecurityModal({ opened, onClose }: SecurityModalProps) {
  const { t, i18n } = useTranslation()
  const { userId } = useAuth()
  const [devices, setDevices] = useState<MLSDevice[] | null>(null)
  const [backedUp, setBackedUp] = useState<boolean | null>(null)
  const [failed, setFailed] = useState(false)
  // The device a "Remove?" confirmation is currently showing for, and the one whose removal is
  // in flight — so the row can ask before acting and disable while it works.
  const [confirming, setConfirming] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)
  const thisDeviceId = loadMlsDeviceId()

  useEffect(() => {
    if (!opened) return
    let active = true
    const load = async () => {
      try {
        const [list, exists] = await Promise.all([api.myDevices(), backupExists()])
        if (!active) return
        setDevices(list)
        setBackedUp(exists)
        setFailed(false)
      } catch {
        if (active) setFailed(true)
      }
    }
    void load()
    return () => {
      active = false
      setConfirming(null)
    }
  }, [opened])

  const removeDevice = async (deviceId: string) => {
    if (!userId) return
    setRemoving(deviceId)
    try {
      await terminateOwnDevice(userId, deviceId)
      setDevices((current) => (current ? current.filter((d) => d.deviceId !== deviceId) : current))
      notifications.show({ color: 'green', message: t('security.removed') })
    } catch {
      notifications.show({ color: 'red', message: t('security.removeFailed') })
    } finally {
      setRemoving(null)
      setConfirming(null)
    }
  }

  return (
    <ResponsiveModal opened={opened} onClose={onClose} title={t('security.title')}>
      <Stack gap="lg">
        <Stack gap="xs">
          <Text fw={600} size="sm">
            {t('security.statusHeading')}
          </Text>
          {backedUp === null ? null : backedUp ? (
            <Alert variant="light" color="green" icon={<IconShieldCheck size={18} />} p="xs">
              <Text size="sm" fw={600}>
                {t('security.backupOn')}
              </Text>
              <Text size="xs" c="dimmed">
                {t('security.backupOnHint')}
              </Text>
            </Alert>
          ) : (
            <Alert variant="light" color="yellow" icon={<IconShieldOff size={18} />} p="xs">
              <Text size="sm" fw={600}>
                {t('security.backupOff')}
              </Text>
              <Text size="xs" c="dimmed">
                {t('security.backupOffHint')}
              </Text>
            </Alert>
          )}
          <Text size="xs" c="dimmed">
            {t('security.syncOn')}
          </Text>
        </Stack>

        <Stack gap="xs">
          <Group justify="space-between">
            <Text fw={600} size="sm">
              {t('security.devicesHeading')}
            </Text>
            {devices && (
              <Text size="xs" c="dimmed">
                {t('security.deviceCount', { count: devices.length })}
              </Text>
            )}
          </Group>

          {failed && (
            <Text size="sm" c="red">
              {t('security.loadFailed')}
            </Text>
          )}
          {!failed && devices === null && <Loader size="sm" />}
          {!failed && devices?.length === 0 && (
            <Text size="sm" c="dimmed">
              {t('security.noDevices')}
            </Text>
          )}
          {devices?.map((d) => {
            const isThis = d.deviceId === thisDeviceId
            return (
              <Group key={d.deviceId} gap="sm" wrap="nowrap" data-testid="security-device">
                <IconDeviceDesktop size={20} />
                <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
                  <Group gap="xs" wrap="nowrap">
                    <Text size="sm" fw={500} truncate>
                      {d.label || d.deviceId.slice(0, 8)}
                    </Text>
                    {isThis && (
                      <Badge size="xs" variant="light" color="iris">
                        {t('security.thisDevice')}
                      </Badge>
                    )}
                  </Group>
                  <Text size="xs" c="dimmed">
                    {t('security.lastActive', { when: relative(d.lastSeenAt, i18n.language) })}
                  </Text>
                </Stack>

                {/* No "remove" on the device you are using — that is what Log out is for. Removing
                    another device is a two-tap confirm: it signs that device out and cuts it out of
                    every encrypted conversation, which is not something to do on a stray tap. */}
                {!isThis &&
                  (confirming === d.deviceId ? (
                    <Group gap="xs" wrap="nowrap">
                      <Button
                        size="compact-xs"
                        color="red"
                        loading={removing === d.deviceId}
                        onClick={() => void removeDevice(d.deviceId)}
                        data-testid="security-device-confirm-remove"
                      >
                        {t('security.confirmRemove')}
                      </Button>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        color="gray"
                        disabled={removing === d.deviceId}
                        onClick={() => setConfirming(null)}
                      >
                        {t('common.cancel')}
                      </Button>
                    </Group>
                  ) : (
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="red"
                      disabled={removing !== null}
                      onClick={() => setConfirming(d.deviceId)}
                      data-testid="security-device-remove"
                    >
                      {t('security.remove')}
                    </Button>
                  ))}
              </Group>
            )
          })}
        </Stack>
      </Stack>
    </ResponsiveModal>
  )
}
