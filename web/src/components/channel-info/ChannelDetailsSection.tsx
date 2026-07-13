import { useState } from 'react'
import { Button, Card, Group, SegmentedControl, Stack, Text, TextInput } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import type { Channel, SubscriptionMode } from '../../lib/types'

interface ChannelDetailsSectionProps {
  channelId: string
  channel: Channel
  onChanged: (next: Channel) => void
}

/**
 * Owner-only: rename the channel, set its phetag, choose how people subscribe.
 *
 * The form seeds itself from `channel` once. Callers mount it with a `key` of the
 * channel id, so opening a different channel in the same pane remounts it with
 * fresh values — which is the reset, no syncing effect required.
 */
export function ChannelDetailsSection({
  channelId,
  channel,
  onChanged,
}: ChannelDetailsSectionProps) {
  const { t } = useTranslation()
  const [name, setName] = useState(channel.name)
  const [mode, setMode] = useState<SubscriptionMode>(channel.subscriptionMode)
  const [alias, setAlias] = useState(channel.alias ?? '')
  const [saving, setSaving] = useState(false)

  async function save() {
    setSaving(true)
    try {
      const updated = await api.updateChannel(channelId, {
        name: name.trim(),
        subscriptionMode: mode,
        alias: alias.trim(),
      })
      onChanged(updated)
      setAlias(updated.alias ?? '')
      notifySuccess(t('channel.channelUpdated'))
    } catch (e) {
      notifyError(t('channel.updateFailed'), e)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card withBorder padding="md">
      <Stack gap="sm">
        <Text fw={600}>{t('channel.detailsTitle')}</Text>
        <TextInput
          label={t('dashboard.channelName')}
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
        />
        <TextInput
          label={t('channel.phetagLabel')}
          placeholder={t('channel.phetagPlaceholder')}
          description={t('channel.phetagHint')}
          leftSection={
            <Text size="sm" c="dimmed">
              @
            </Text>
          }
          value={alias}
          onChange={(e) => setAlias(e.currentTarget.value)}
        />
        <div>
          <Text size="sm" fw={500} mb={4}>
            {t('channel.subscriptionMode')}
          </Text>
          <SegmentedControl
            fullWidth
            value={mode}
            onChange={(v) => setMode(v as SubscriptionMode)}
            data={[
              { label: t('mode.approval'), value: 'approval' },
              { label: t('mode.open'), value: 'open' },
            ]}
          />
        </div>
        <Group justify="flex-end">
          <Button onClick={save} loading={saving} disabled={!name.trim()}>
            {t('channel.saveChanges')}
          </Button>
        </Group>
      </Stack>
    </Card>
  )
}
