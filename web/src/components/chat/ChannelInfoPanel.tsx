import { ActionIcon, Card, Stack, Text, Title } from '@mantine/core'
import { IconX } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { ChannelDetailsSection } from '../channel-info/ChannelDetailsSection'
import { ChannelShareSection } from '../channel-info/ChannelShareSection'
import { ChannelKeysSection } from '../channel-info/ChannelKeysSection'
import { ChannelPushSection } from '../channel-info/ChannelPushSection'
import { ChannelDangerSection } from '../channel-info/ChannelDangerSection'
import { SubscribersPanel } from '../SubscribersPanel'
import { ModeBadge } from '../badges'
import { ChannelAvatarPicker } from '../channel-info/ChannelAvatarPicker'
import type { Channel } from '../../lib/types'

interface ChannelInfoPanelProps {
  channelId: string
  channel: Channel
  isOwner: boolean
  canModerate: boolean
  onClose: () => void
  onChannelChanged: (next: Channel) => void
  /** Re-reads the chat list after a rename, delete or leave. */
  onListChanged: () => Promise<void>
}

/**
 * Telegram's channel-info column, reached from the ⋮ in the chat header. It holds
 * everything the old Settings tab did; each section is rendered only for the
 * roles that may act on it.
 */
export function ChannelInfoPanel({
  channelId,
  channel,
  isOwner,
  canModerate,
  onClose,
  onChannelChanged,
  onListChanged,
}: ChannelInfoPanelProps) {
  const { t } = useTranslation()

  return (
    <aside className="pheme-info" data-open="true" data-testid="channel-info">
      <div className="pheme-info-header">
        <Title order={6} style={{ flex: 1 }}>
          {t('channel.info')}
        </Title>
        <ActionIcon
          variant="subtle"
          color="gray"
          aria-label={t('channel.closeInfo')}
          onClick={onClose}
        >
          <IconX size={18} />
        </ActionIcon>
      </div>

      <div className="pheme-info-body">
        <Stack gap="md">
          <Stack align="center" gap={4} py="sm">
            <ChannelAvatarPicker
              channel={channel}
              canEdit={isOwner}
              onChanged={(next) => {
                onChannelChanged(next)
                // The sidebar row and the chat header show the picture too.
                void onListChanged()
              }}
            />
            <Text fw={600}>{channel.name}</Text>
            {channel.alias && (
              <Text size="sm" c="dimmed">
                @{channel.alias}
              </Text>
            )}
            <ModeBadge mode={channel.subscriptionMode} />
          </Stack>

          {isOwner && (
            <>
              {/* Keyed: switching channels with the panel open remounts the form
                  with the new channel's values instead of syncing them. */}
              <ChannelDetailsSection
                key={channelId}
                channelId={channelId}
                channel={channel}
                onChanged={(next) => {
                  onChannelChanged(next)
                  void onListChanged()
                }}
              />
              <ChannelShareSection channel={channel} />
            </>
          )}

          {canModerate && (
            <Card withBorder padding="md">
              <Stack gap="sm">
                <Text fw={600}>{t('channel.tabs.subscribers')}</Text>
                <SubscribersPanel channelId={channelId} />
              </Stack>
            </Card>
          )}

          {isOwner && <ChannelKeysSection channelId={channelId} />}

          <ChannelPushSection channelId={channelId} />

          <ChannelDangerSection
            channelId={channelId}
            channel={channel}
            isOwner={isOwner}
            onChanged={onListChanged}
          />
        </Stack>
      </div>
    </aside>
  )
}
