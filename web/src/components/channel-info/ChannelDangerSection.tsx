import { useState } from 'react'
import { Button, Card, Group, Stack, Text } from '@mantine/core'
import { IconLogout } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import { forgetSeen } from '../../lib/lastSeen'
import { ConfirmModal } from '../ConfirmModal'
import type { Channel } from '../../lib/types'

interface ChannelDangerSectionProps {
  channelId: string
  channel: Channel
  isOwner: boolean
  /** Re-reads the chat list once the channel is gone from it. */
  onChanged: () => Promise<void>
}

/** Delete the channel (owner) or leave it (member). */
export function ChannelDangerSection({
  channelId,
  channel,
  isOwner,
  onChanged,
}: ChannelDangerSectionProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)

  async function run() {
    setBusy(true)
    try {
      if (isOwner) {
        await api.deleteChannel(channelId)
        notifySuccess(t('channel.channelDeleted'))
      } else {
        await api.leaveChannel(channelId)
        notifySuccess(t('channel.left'))
      }
      forgetSeen(channelId)
      await onChanged()
      navigate('/', { replace: true })
    } catch (e) {
      notifyError(isOwner ? t('channel.deleteFailed') : t('channel.leaveFailed'), e)
      setBusy(false)
    }
  }

  return (
    <>
      <ConfirmModal
        opened={confirming}
        onClose={() => setConfirming(false)}
        onConfirm={run}
        title={isOwner ? t('channel.deleteTitle') : t('channel.leave')}
        confirmLabel={isOwner ? t('channel.deleteTitle') : t('channel.leave')}
        loading={busy}
      >
        <Text size="sm">
          <Trans
            i18nKey={isOwner ? 'channel.deleteConfirm' : 'channel.leaveConfirm'}
            values={{ name: channel.name }}
            components={{ bold: <b /> }}
          />
        </Text>
      </ConfirmModal>

      <Card withBorder padding="md" style={{ borderColor: 'var(--mantine-color-red-4)' }}>
        {/* Wraps rather than squeezing: on a narrow phone the description text
            was compressing the button until its label was cut off ("Delet"). */}
        <Group justify="space-between" wrap="wrap" gap="sm">
          <Stack gap={2} style={{ flex: '1 1 12rem', minWidth: 0 }}>
            <Text fw={600}>{isOwner ? t('channel.dangerTitle') : t('channel.leaveTitle')}</Text>
            <Text size="sm" c="dimmed">
              {isOwner ? t('channel.dangerDescription') : t('channel.leaveDescription')}
            </Text>
          </Stack>
          <Button
            color="red"
            variant="outline"
            style={{ flexShrink: 0 }}
            leftSection={isOwner ? undefined : <IconLogout size={16} />}
            onClick={() => setConfirming(true)}
          >
            {isOwner ? t('common.delete') : t('channel.leave')}
          </Button>
        </Group>
      </Card>
    </>
  )
}
